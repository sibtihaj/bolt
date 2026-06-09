package docker

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/sibtihaj/bolt/app/credentials"
	runner "github.com/sibtihaj/bolt/internal/exec"
	"github.com/sibtihaj/bolt/app/state"
)

// projectName returns the Docker Compose project name for a deployment.
func projectName(name string) string {
	return "tfe-" + name
}

// dockerEnv builds the env slice for docker compose commands, including
// DOCKER_HOST for remote deployments and all TFE credential variables.
func dockerEnv(d *state.TFEDeployment, creds *credentials.TFECredentials) []string {
	env := os.Environ()
	if d.SSHHost != "" {
		user := d.SSHUser
		if user == "" {
			user = os.Getenv("USER")
		}
		dockerHost := fmt.Sprintf("ssh://%s@%s", user, d.SSHHost)
		env = append(env, "DOCKER_HOST="+dockerHost)
		if d.SSHKeyPath != "" {
			env = append(env, "SSH_AUTH_SOCK=") // clear agent; key used directly by ssh
		}
	}
	// Inject credentials as env vars so ${VAR} references in compose file resolve
	env = append(env,
		"TFE_LICENSE="+creds.License,
		"TFE_ENCRYPTION_PASSWORD="+creds.EncryptionPassword,
	)
	if creds.DatabaseURL != "" {
		env = append(env, "TFE_DATABASE_URL="+creds.DatabaseURL)
	}
	if creds.S3Bucket != "" {
		env = append(env,
			"TFE_S3_BUCKET="+creds.S3Bucket,
			"TFE_S3_REGION="+creds.S3Region,
			"TFE_S3_ACCESS_KEY_ID="+creds.S3AccessKeyID,
			"TFE_S3_SECRET_ACCESS_KEY="+creds.S3SecretAccessKey,
		)
	}
	if creds.RedisURL != "" {
		env = append(env, "TFE_REDIS_URL="+creds.RedisURL)
	}
	return env
}

// CheckPrereqs verifies docker and the compose plugin are available.
func CheckPrereqs() error {
	if _, err := runner.Output("docker", []string{"info"}, runner.RunOptions{}); err != nil {
		return fmt.Errorf("docker not running or not installed: %w", err)
	}
	out, err := runner.Output("docker", []string{"compose", "version"}, runner.RunOptions{})
	if err != nil {
		return fmt.Errorf("docker compose plugin not found — install Docker Desktop or the compose plugin: %w", err)
	}
	fmt.Printf("docker compose: %s\n", strings.TrimSpace(string(out)))
	return nil
}

// ComposeUp runs docker compose up --detach --wait (idempotent).
// capture, if non-nil, receives a copy of stderr for error classification by
// the retry layer.
func ComposeUp(d *state.TFEDeployment, creds *credentials.TFECredentials, composePath, waitTimeout string, capture *bytes.Buffer) error {
	args := []string{
		"compose",
		"--file", composePath,
		"--project-name", projectName(d.Name),
		"up", "--detach", "--wait",
		"--wait-timeout", waitTimeout,
	}
	return runner.Run("docker", args, runner.RunOptions{
		Env:           dockerEnv(d, creds),
		StderrCapture: capture,
	})
}

// ComposeDown runs docker compose down -v (removes containers and volumes).
func ComposeDown(d *state.TFEDeployment) error {
	home, _ := os.UserHomeDir()
	composePath := home + "/.bolt/compose/" + d.Name + "/docker-compose.yaml"

	args := []string{
		"compose",
		"--file", composePath,
		"--project-name", projectName(d.Name),
		"down", "-v",
	}
	env := os.Environ()
	if d.SSHHost != "" {
		user := d.SSHUser
		if user == "" {
			user = os.Getenv("USER")
		}
		env = append(env, "DOCKER_HOST=ssh://"+user+"@"+d.SSHHost)
	}
	return runner.Run("docker", args, runner.RunOptions{Env: env})
}

// ComposePs prints the running containers for the deployment.
func ComposePs(d *state.TFEDeployment) error {
	home, _ := os.UserHomeDir()
	composePath := home + "/.bolt/compose/" + d.Name + "/docker-compose.yaml"

	args := []string{
		"compose",
		"--file", composePath,
		"--project-name", projectName(d.Name),
		"ps",
	}
	env := os.Environ()
	if d.SSHHost != "" {
		user := d.SSHUser
		if user == "" {
			user = os.Getenv("USER")
		}
		env = append(env, "DOCKER_HOST=ssh://"+user+"@"+d.SSHHost)
	}
	return runner.Run("docker", args, runner.RunOptions{Env: env})
}
