package kubectl

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	runner "github.com/sibtihaj/bolt/internal/exec"
	"github.com/sibtihaj/bolt/app/state"
)

func args(d *state.TFEDeployment, extra ...string) []string {
	base := extra
	if d.Kubeconfig != "" {
		base = append(base, "--kubeconfig", d.Kubeconfig)
	}
	return base
}

func env(d *state.TFEDeployment) []string {
	var e []string
	if d.Kubeconfig != "" {
		e = append(e, "KUBECONFIG="+d.Kubeconfig)
	}
	e = append(e, d.ExtraEnv...)
	return e
}

// CheckPrereqs verifies kubectl is on PATH.
func CheckPrereqs() error {
	out, err := runner.Output("kubectl", []string{"version", "--client"}, runner.RunOptions{})
	if err != nil {
		return fmt.Errorf("kubectl not found — install it from https://kubernetes.io/docs/tasks/tools/: %w", err)
	}
	fmt.Printf("kubectl: %s\n", strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	return nil
}

// CreateNamespace creates the namespace, silently ignoring "already exists".
func CreateNamespace(d *state.TFEDeployment) error {
	var stderr bytes.Buffer
	a := args(d, "create", "namespace", d.Namespace)
	err := runner.Run("kubectl", a, runner.RunOptions{Env: env(d), StderrCapture: &stderr})
	if err != nil && strings.Contains(stderr.String(), "AlreadyExists") {
		return nil
	}
	return err
}

// DeleteNamespace removes the namespace and all resources in it.
func DeleteNamespace(d *state.TFEDeployment) error {
	a := args(d, "delete", "namespace", d.Namespace, "--ignore-not-found")
	return runner.Run("kubectl", a, runner.RunOptions{Env: env(d)})
}

// UpsertSecret deletes then recreates a generic secret (idempotent).
// data maps secret key → value (plain text; kubectl encodes to base64).
func UpsertSecret(d *state.TFEDeployment, secretName string, data map[string]string) error {
	// Delete first (ignore not-found)
	delArgs := args(d, "delete", "secret", secretName,
		"--namespace", d.Namespace, "--ignore-not-found")
	if err := runner.Run("kubectl", delArgs, runner.RunOptions{Env: env(d)}); err != nil {
		return fmt.Errorf("delete secret %s: %w", secretName, err)
	}

	// Build create args
	createArgs := []string{"create", "secret", "generic", secretName, "--namespace", d.Namespace}
	for k, v := range data {
		createArgs = append(createArgs, fmt.Sprintf("--from-literal=%s=%s", k, v))
	}
	createArgs = append(createArgs, args(d)...)
	if err := runner.Run("kubectl", createArgs, runner.RunOptions{Env: env(d)}); err != nil {
		return fmt.Errorf("create secret %s: %w", secretName, err)
	}
	return nil
}

// UpsertTLSSecret creates a tls secret from cert and key file paths.
func UpsertTLSSecret(d *state.TFEDeployment, secretName, certPath, keyPath string) error {
	delArgs := args(d, "delete", "secret", secretName,
		"--namespace", d.Namespace, "--ignore-not-found")
	if err := runner.Run("kubectl", delArgs, runner.RunOptions{Env: env(d)}); err != nil {
		return fmt.Errorf("delete secret %s: %w", secretName, err)
	}
	createArgs := args(d,
		"create", "secret", "tls", secretName,
		"--namespace", d.Namespace,
		"--cert", certPath,
		"--key", keyPath,
	)
	return runner.Run("kubectl", createArgs, runner.RunOptions{Env: env(d)})
}

// ValidateRegistryCredentials does a lightweight HTTP probe to verify that
// username/password are accepted by images.releases.hashicorp.com before
// creating the Kubernetes secret — failing fast is friendlier than waiting for
// pods to enter ImagePullBackOff.
func ValidateRegistryCredentials(username, password string) error {
	req, err := http.NewRequest(http.MethodGet, "https://images.releases.hashicorp.com/v2/", nil)
	if err != nil {
		return fmt.Errorf("build registry probe request: %w", err)
	}
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	req.Header.Set("Authorization", "Basic "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("reach images.releases.hashicorp.com: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf(
			"registry authentication failed (HTTP %d) — set TFE_REGISTRY_USERNAME and TFE_REGISTRY_PASSWORD to your images.releases.hashicorp.com credentials",
			resp.StatusCode,
		)
	}
	return nil
}

// UpsertImagePullSecret creates (or recreates) a docker-registry secret used to
// pull TFE images from images.releases.hashicorp.com.
func UpsertImagePullSecret(d *state.TFEDeployment, secretName, username, password string) error {
	delArgs := args(d, "delete", "secret", secretName,
		"--namespace", d.Namespace, "--ignore-not-found")
	if err := runner.Run("kubectl", delArgs, runner.RunOptions{Env: env(d)}); err != nil {
		return fmt.Errorf("delete secret %s: %w", secretName, err)
	}
	createArgs := args(d,
		"create", "secret", "docker-registry", secretName,
		"--namespace", d.Namespace,
		"--docker-server=images.releases.hashicorp.com",
		"--docker-username="+username,
		"--docker-password="+password,
	)
	if err := runner.Run("kubectl", createArgs, runner.RunOptions{Env: env(d)}); err != nil {
		return fmt.Errorf("create image pull secret %s: %w", secretName, err)
	}
	return nil
}

// GetPods prints the pods in the TFE namespace to stdout.
func GetPods(d *state.TFEDeployment) error {
	a := args(d, "get", "pods", "--namespace", d.Namespace)
	return runner.Run("kubectl", a, runner.RunOptions{Env: env(d)})
}

// AnyPodsPending returns true when at least one pod in the namespace is not
// yet Running — i.e. still pulling the image or initialising containers.
func AnyPodsPending(d *state.TFEDeployment) bool {
	out, err := runner.Output("kubectl", args(d,
		"get", "pods",
		"--namespace", d.Namespace,
		"--no-headers",
	), runner.RunOptions{Env: env(d)})
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Pending") ||
			strings.Contains(line, "ContainerCreating") ||
			strings.Contains(line, "Init:") ||
			strings.Contains(line, "PodInitializing") {
			return true
		}
	}
	return false
}

// PrintPodEvents prints recent Kubernetes events for the namespace, sorted by
// timestamp.  Used while pods are pending so the user can see image-pull
// progress and other lifecycle events instead of a static "Pending" status.
func PrintPodEvents(d *state.TFEDeployment) error {
	a := args(d,
		"get", "events",
		"--namespace", d.Namespace,
		"--sort-by=.lastTimestamp",
	)
	return runner.Run("kubectl", a, runner.RunOptions{Env: env(d)})
}
