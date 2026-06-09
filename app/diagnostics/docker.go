package diagnostics

import (
	"fmt"
	"os"

	runner "github.com/sibtihaj/bolt/internal/exec"
)

// DiagnoseDocker collects Docker Compose diagnostics after a failed deploy.
func DiagnoseDocker(composePath, projectName string) {
	fmt.Println("\n⚠  Collecting failure diagnostics...")
	dockerComposePs(composePath, projectName)
	dockerComposeLogs(composePath, projectName)
}

func dockerComposePs(composePath, projectName string) {
	fmt.Printf("\n─── Container Status (%s) ───\n", projectName)
	_ = runner.Run("docker", []string{
		"compose",
		"--file", composePath,
		"--project-name", projectName,
		"ps",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}

func dockerComposeLogs(composePath, projectName string) {
	fmt.Printf("\n─── Container Logs — last 50 lines (%s) ───\n", projectName)
	_ = runner.Run("docker", []string{
		"compose",
		"--file", composePath,
		"--project-name", projectName,
		"logs", "--tail", "50", "--no-color",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}
