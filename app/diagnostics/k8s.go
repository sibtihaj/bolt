package diagnostics

import (
	"fmt"
	"os"

	runner "github.com/sibtihaj/bolt/internal/exec"
)

// DiagnoseK8s collects Kubernetes and Helm diagnostics after a failed deploy
// and prints them to stdout so the operator can understand what went wrong.
func DiagnoseK8s(namespace, releaseName string) {
	fmt.Println("\n⚠  Collecting failure diagnostics...")
	k8sWarningEvents(namespace)
	k8sDescribePods(namespace)
	helmHistory(releaseName, namespace)
}

func k8sWarningEvents(namespace string) {
	fmt.Printf("\n─── Kubernetes Warning Events (ns: %s) ───\n", namespace)
	_ = runner.Run("kubectl", []string{
		"get", "events",
		"--namespace", namespace,
		"--field-selector", "type=Warning",
		"--sort-by", ".metadata.creationTimestamp",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}

func k8sDescribePods(namespace string) {
	fmt.Printf("\n─── Pod Status (ns: %s) ───\n", namespace)
	_ = runner.Run("kubectl", []string{
		"get", "pods",
		"--namespace", namespace,
		"-o", "wide",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})

	fmt.Printf("\n─── Pod Describe (ns: %s) ───\n", namespace)
	_ = runner.Run("kubectl", []string{
		"describe", "pods",
		"--namespace", namespace,
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}

func helmHistory(releaseName, namespace string) {
	fmt.Printf("\n─── Helm History (%s / ns: %s) ───\n", releaseName, namespace)
	_ = runner.Run("helm", []string{
		"history", releaseName,
		"--namespace", namespace,
		"--max", "5",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}
