package helm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runner "github.com/sibtihaj/bolt/internal/exec"
	"github.com/sibtihaj/bolt/app/state"
)

const hashicorpRepo = "https://helm.releases.hashicorp.com"

// base returns common kubectl/helm env + kubeconfig flag args for a deployment.
func kubeconfigArgs(d *state.TFEDeployment) []string {
	if d.Kubeconfig != "" {
		return []string{"--kubeconfig", d.Kubeconfig}
	}
	return nil
}

func kubeconfigEnv(d *state.TFEDeployment) []string {
	if d.Kubeconfig != "" {
		return []string{"KUBECONFIG=" + d.Kubeconfig}
	}
	return nil
}

// RepoAdd adds the HashiCorp Helm repo (idempotent — ignores "already exists").
func RepoAdd(d *state.TFEDeployment) error {
	args := append([]string{"repo", "add", "hashicorp", hashicorpRepo}, kubeconfigArgs(d)...)
	err := runner.Run("helm", args, runner.RunOptions{Env: kubeconfigEnv(d)})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return nil
}

// RepoUpdate refreshes the HashiCorp repo index.
func RepoUpdate(d *state.TFEDeployment) error {
	args := append([]string{"repo", "update", "hashicorp"}, kubeconfigArgs(d)...)
	return runner.Run("helm", args, runner.RunOptions{Env: kubeconfigEnv(d)})
}

// WriteValues renders values.yaml for d and writes it to ~/.bolt/helm/<name>/values.yaml.
// Returns the path written.
func WriteValues(d *state.TFEDeployment) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".bolt", "helm", d.Name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	content, err := BuildValues(d)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// Install runs helm upgrade --install (idempotent). It streams output live.
func Install(d *state.TFEDeployment, valuesPath, timeout string) error {
	args := []string{
		"upgrade", "--install", "tfe",
		"hashicorp/terraform-enterprise",
		"--namespace", d.Namespace,
		"--values", valuesPath,
		"--wait",
		"--timeout", timeout,
	}
	if d.HelmChartVersion != "" {
		args = append(args, "--version", d.HelmChartVersion)
	}
	args = append(args, kubeconfigArgs(d)...)
	return runner.Run("helm", args, runner.RunOptions{Env: kubeconfigEnv(d)})
}

// Uninstall removes the TFE Helm release from the namespace.
func Uninstall(d *state.TFEDeployment) error {
	args := append([]string{"uninstall", "tfe", "--namespace", d.Namespace}, kubeconfigArgs(d)...)
	err := runner.Run("helm", args, runner.RunOptions{Env: kubeconfigEnv(d)})
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}

// CheckPrereqs verifies helm is on PATH with a version call.
func CheckPrereqs() error {
	out, err := runner.Output("helm", []string{"version", "--short"}, runner.RunOptions{})
	if err != nil {
		return fmt.Errorf("helm not found — install it from https://helm.sh/docs/intro/install/: %w", err)
	}
	fmt.Printf("helm %s\n", strings.TrimSpace(string(out)))
	return nil
}
