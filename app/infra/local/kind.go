package local

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	runner "github.com/sibtihaj/bolt/internal/exec"
)

// EnsureKindCluster creates a kind cluster named bolt-<namePrefix> and writes
// its kubeconfig to ~/.bolt/kubeconfigs/<namePrefix>.yaml.
// Returns the kubeconfig path. Idempotent — reuses the cluster if it already exists.
// When prompt is true (interactive TUI flow), the user is asked before installing kind.
// When prompt is false (non-interactive CLI), kind is installed automatically.
func EnsureKindCluster(namePrefix string, prompt bool) (string, error) {
	clusterName := "bolt-" + namePrefix

	if err := checkKindPrereqs(prompt); err != nil {
		return "", err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	kubeconfigDir := filepath.Join(home, ".bolt", "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0700); err != nil {
		return "", fmt.Errorf("create kubeconfigs dir: %w", err)
	}
	kubeconfigPath := filepath.Join(kubeconfigDir, namePrefix+".yaml")

	exists, err := kindClusterExists(clusterName)
	if err != nil {
		return "", err
	}

	if exists {
		fmt.Printf("  ↺  Reusing existing kind cluster %s — exporting kubeconfig…\n", clusterName)
		if err := runner.Run("kind", []string{
			"export", "kubeconfig",
			"--name", clusterName,
			"--kubeconfig", kubeconfigPath,
		}, runner.RunOptions{Stderr: os.Stderr}); err != nil {
			return "", fmt.Errorf("export kind kubeconfig: %w", err)
		}
		return kubeconfigPath, nil
	}

	if err := runner.Run("kind", []string{
		"create", "cluster",
		"--name", clusterName,
		"--kubeconfig", kubeconfigPath,
	}, runner.RunOptions{Stderr: os.Stderr}); err != nil {
		return "", fmt.Errorf("kind create cluster: %w", err)
	}

	return kubeconfigPath, nil
}

// DeleteKindCluster deletes the kind cluster for the given name prefix.
func DeleteKindCluster(namePrefix string) error {
	clusterName := "bolt-" + namePrefix
	exists, err := kindClusterExists(clusterName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return runner.Run("kind", []string{"delete", "cluster", "--name", clusterName}, runner.RunOptions{})
}

func checkKindPrereqs(prompt bool) error {
	if _, err := runner.Output("docker", []string{"info"}, runner.RunOptions{}); err != nil {
		return fmt.Errorf("Docker is not running — start Docker Desktop and retry")
	}
	if _, err := runner.Output("kind", []string{"version"}, runner.RunOptions{}); err != nil {
		return installKind(prompt)
	}
	return nil
}

func installKind(prompt bool) error {
	if prompt {
		fmt.Print("  kind is not installed. Install it now? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("kind not found — install it from https://kind.sigs.k8s.io/docs/user/quick-start/#installation")
		}
	} else {
		fmt.Println("  kind is not installed — installing automatically…")
	}

	switch runtime.GOOS {
	case "darwin":
		brew := "/opt/homebrew/bin/brew"
		if _, statErr := os.Stat(brew); statErr != nil {
			brew = "/usr/local/bin/brew" // Intel Mac fallback
		}
		fmt.Println("  Running: brew install kind")
		if err := runner.Run(brew, []string{"install", "kind"}, runner.RunOptions{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}); err != nil {
			return fmt.Errorf("brew install kind failed: %w", err)
		}
		fmt.Println("  kind installed successfully.")
		return nil
	case "linux":
		fmt.Println("  Running: go install sigs.k8s.io/kind@latest")
		if err := runner.Run("go", []string{"install", "sigs.k8s.io/kind@latest"}, runner.RunOptions{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}); err != nil {
			return fmt.Errorf("kind install failed: %w\n  Manual install: https://kind.sigs.k8s.io/docs/user/quick-start/#installation", err)
		}
		fmt.Println("  kind installed successfully.")
		return nil
	default:
		return fmt.Errorf("automatic kind installation is not supported on %s — install manually: https://kind.sigs.k8s.io/docs/user/quick-start/#installation", runtime.GOOS)
	}
}

func kindClusterExists(clusterName string) (bool, error) {
	out, err := runner.Output("kind", []string{"get", "clusters"}, runner.RunOptions{})
	if err != nil {
		return false, fmt.Errorf("kind get clusters: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == clusterName {
			return true, nil
		}
	}
	return false, nil
}
