package kubectl

import (
	"fmt"
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
	if d.Kubeconfig != "" {
		return []string{"KUBECONFIG=" + d.Kubeconfig}
	}
	return nil
}

// CheckPrereqs verifies kubectl is on PATH.
func CheckPrereqs() error {
	out, err := runner.Output("kubectl", []string{"version", "--client", "--short"}, runner.RunOptions{})
	if err != nil {
		// --short was deprecated in 1.28; try without it
		out, err = runner.Output("kubectl", []string{"version", "--client"}, runner.RunOptions{})
		if err != nil {
			return fmt.Errorf("kubectl not found — install it from https://kubernetes.io/docs/tasks/tools/: %w", err)
		}
	}
	fmt.Printf("kubectl: %s\n", strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	return nil
}

// CreateNamespace creates the namespace, silently ignoring "already exists".
func CreateNamespace(d *state.TFEDeployment) error {
	a := args(d, "create", "namespace", d.Namespace)
	err := runner.Run("kubectl", a, runner.RunOptions{Env: env(d)})
	if err != nil && strings.Contains(err.Error(), "already exists") {
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

// GetPods prints the pods in the TFE namespace to stdout.
func GetPods(d *state.TFEDeployment) error {
	a := args(d, "get", "pods", "--namespace", d.Namespace)
	return runner.Run("kubectl", a, runner.RunOptions{Env: env(d)})
}
