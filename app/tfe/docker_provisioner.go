package tfe

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sibtihaj/bolt/app/credentials"
	appDocker "github.com/sibtihaj/bolt/app/docker"
	appTLS "github.com/sibtihaj/bolt/app/tls"
	"github.com/sibtihaj/bolt/app/state"
)

type DockerProvisioner struct {
	deployment *state.TFEDeployment
}

func (p *DockerProvisioner) Deploy(creds *credentials.TFECredentials) error {
	d := p.deployment

	// 1. Prerequisite check
	fmt.Println("→ Checking prerequisites...")
	if err := appDocker.CheckPrereqs(); err != nil {
		return err
	}

	// 2. Data directory (disk mode only)
	if d.Mode == state.ModeDisk {
		fmt.Printf("→ Preparing data directory %s...\n", d.DataDir)
		if err := os.MkdirAll(filepath.Join(d.DataDir, "tfe"), 0700); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
	}

	// 3. TLS
	if d.SelfSignedTLS {
		fmt.Println("→ Generating self-signed TLS certificate...")
		home, _ := os.UserHomeDir()
		tlsDir := filepath.Join(home, ".bolt", "tls", d.Name)
		certPath := filepath.Join(tlsDir, "tfe.crt")
		keyPath := filepath.Join(tlsDir, "tfe.key")
		if err := appTLS.GenerateSelfSignedCert(d.Hostname, certPath, keyPath); err != nil {
			return fmt.Errorf("generate TLS: %w", err)
		}
		d.TLSCertPath = certPath
		d.TLSKeyPath = keyPath
	} else {
		// Validate the provided files exist
		if d.TLSCertPath == "" || d.TLSKeyPath == "" {
			return fmt.Errorf("--tls-cert and --tls-key are required (or use --generate-tls for a self-signed cert)")
		}
		for _, path := range []string{d.TLSCertPath, d.TLSKeyPath} {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("TLS file not found: %s", path)
			}
		}
	}

	// 4. Generate compose file
	fmt.Println("→ Generating docker-compose.yaml...")
	composePath, err := appDocker.WriteCompose(d)
	if err != nil {
		return fmt.Errorf("generate compose: %w", err)
	}

	// 5. docker compose up (streams live output; credentials injected via env)
	fmt.Println("→ Starting Terraform Enterprise (this may take several minutes)...")
	waitTimeout := "600" // seconds
	if err := appDocker.ComposeUp(d, creds, composePath, waitTimeout); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}

	// 6. Save state
	d.Status = state.StatusRunning
	d.UpdatedAt = time.Now()
	if err := state.Save(d); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	// 7. Health summary
	fmt.Println("\n→ Deployment complete. Container status:")
	_ = appDocker.ComposePs(d)
	fmt.Printf("\nTFE URL: https://%s\n", d.Hostname)
	fmt.Println("Note: TFE may take a few minutes to initialize after containers are running.")
	_ = strconv.Itoa(0) // satisfy import
	return nil
}

func (p *DockerProvisioner) Destroy(force bool) error {
	d := p.deployment
	fmt.Println("→ Stopping and removing containers...")
	if err := appDocker.ComposeDown(d); err != nil && !force {
		return err
	}
	if err := state.Delete(d.Name); err != nil {
		return fmt.Errorf("remove state: %w", err)
	}
	fmt.Printf("Deployment %q destroyed.\n", d.Name)
	return nil
}

func (p *DockerProvisioner) Status() (*ProvisionerStatus, error) {
	d := p.deployment
	if err := appDocker.ComposePs(d); err != nil {
		return &ProvisionerStatus{
			DeploymentStatus: state.StatusUnknown,
			Message:          err.Error(),
		}, nil
	}
	return &ProvisionerStatus{
		DeploymentStatus: d.Status,
		URL:              fmt.Sprintf("https://%s", d.Hostname),
	}, nil
}
