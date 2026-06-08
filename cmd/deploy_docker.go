package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
)

var dockerOpts struct {
	Name               string
	Mode               string
	Hostname           string
	DataDir            string
	SSHHost            string
	SSHUser            string
	SSHKeyPath         string
	ImageTag           string
	WaitTimeout        string
	DryRun             bool
	GenerateTLS        bool
	// Credential flags
	License            string
	LicensePath        string
	EncryptionPassword string
	TLSCertPath        string
	TLSKeyPath         string
	DatabaseURL        string
	S3Bucket           string
	S3Region           string
	S3AccessKeyID      string
	S3SecretAccessKey  string
	RedisURL           string
}

var deployDockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Deploy TFE using Docker Compose",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		validModes := map[string]bool{"disk": true, "external": true, "active-active": true}
		if !validModes[dockerOpts.Mode] {
			return fmt.Errorf("--mode must be disk, external, or active-active (got %q)", dockerOpts.Mode)
		}
		if dockerOpts.Mode != "disk" && dockerOpts.DatabaseURL == "" && os.Getenv("TFE_DATABASE_URL") == "" {
			return fmt.Errorf("--db-url (or TFE_DATABASE_URL) is required for mode %q", dockerOpts.Mode)
		}
		if dockerOpts.Mode == "active-active" && dockerOpts.RedisURL == "" && os.Getenv("TFE_REDIS_URL") == "" {
			return fmt.Errorf("--redis-url (or TFE_REDIS_URL) is required for active-active mode")
		}
		if !dockerOpts.GenerateTLS && (dockerOpts.TLSCertPath == "" || dockerOpts.TLSKeyPath == "") {
			return fmt.Errorf("provide --tls-cert and --tls-key, or use --generate-tls")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := credentials.Resolve(credentials.Flags{
			License: dockerOpts.License, LicensePath: dockerOpts.LicensePath,
			EncryptionPassword: dockerOpts.EncryptionPassword,
			TLSCertPath: dockerOpts.TLSCertPath, TLSKeyPath: dockerOpts.TLSKeyPath,
			DatabaseURL: dockerOpts.DatabaseURL, S3Bucket: dockerOpts.S3Bucket,
			S3Region: dockerOpts.S3Region, S3AccessKeyID: dockerOpts.S3AccessKeyID,
			S3SecretAccessKey: dockerOpts.S3SecretAccessKey, RedisURL: dockerOpts.RedisURL,
		}, globalConfig)
		if err != nil {
			return err
		}

		imageTag := dockerOpts.ImageTag
		if imageTag == "" {
			imageTag = "latest"
		}

		dataDir := dockerOpts.DataDir
		if dataDir == "" {
			home, _ := os.UserHomeDir()
			dataDir = filepath.Join(home, ".bolt", "data", dockerOpts.Name)
		}

		d := &state.TFEDeployment{
			Name:          dockerOpts.Name,
			Backend:       state.BackendDocker,
			Mode:          state.OperationalMode(dockerOpts.Mode),
			Hostname:      dockerOpts.Hostname,
			ImageTag:      imageTag,
			TLSCertPath:   creds.TLSCert,
			TLSKeyPath:    creds.TLSKey,
			SelfSignedTLS: dockerOpts.GenerateTLS,
			DataDir:       dataDir,
			SSHHost:       dockerOpts.SSHHost,
			SSHUser:       dockerOpts.SSHUser,
			SSHKeyPath:    dockerOpts.SSHKeyPath,
			Status:        state.StatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		if dockerOpts.S3Bucket != "" || creds.S3Bucket != "" {
			d.StorageConfig = &state.StorageConfig{S3Bucket: creds.S3Bucket, S3Region: creds.S3Region}
		}

		p, err := tfe.NewProvisioner(d)
		if err != nil {
			return err
		}
		return p.Deploy(creds)
	},
}

func init() {
	deployCmd.AddCommand(deployDockerCmd)

	f := deployDockerCmd.Flags()
	f.StringVarP(&dockerOpts.Name, "name", "n", "", "deployment name (required)")
	f.StringVar(&dockerOpts.Mode, "mode", "disk", "operational mode: disk, external, active-active")
	f.StringVar(&dockerOpts.Hostname, "hostname", "", "TFE FQDN (required)")
	f.StringVar(&dockerOpts.DataDir, "data-dir", "", "host data directory for disk mode (default: ~/.bolt/data/<name>)")
	f.StringVar(&dockerOpts.SSHHost, "ssh-host", "", "deploy to a remote Docker host via SSH")
	f.StringVar(&dockerOpts.SSHUser, "ssh-user", "", "SSH user for remote Docker host")
	f.StringVar(&dockerOpts.SSHKeyPath, "ssh-key", "", "SSH private key path for remote Docker host")
	f.StringVar(&dockerOpts.ImageTag, "image-tag", "latest", "TFE container image tag")
	f.StringVar(&dockerOpts.WaitTimeout, "wait-timeout", "600", "docker compose --wait-timeout in seconds")
	f.BoolVar(&dockerOpts.DryRun, "dry-run", false, "render docker-compose.yaml and exit without deploying")
	f.BoolVar(&dockerOpts.GenerateTLS, "generate-tls", false, "generate a self-signed TLS certificate (dev only)")
	// Credentials
	f.StringVar(&dockerOpts.License, "license", "", "TFE license string (or TFE_LICENSE env)")
	f.StringVar(&dockerOpts.LicensePath, "license-path", "", "path to TFE license file (or TFE_LICENSE_PATH env)")
	f.StringVar(&dockerOpts.EncryptionPassword, "encryption-password", "", "TFE encryption password")
	f.StringVar(&dockerOpts.TLSCertPath, "tls-cert", "", "path to TLS certificate PEM file")
	f.StringVar(&dockerOpts.TLSKeyPath, "tls-key", "", "path to TLS private key PEM file")
	f.StringVar(&dockerOpts.DatabaseURL, "db-url", "", "PostgreSQL connection URL (external/active-active)")
	f.StringVar(&dockerOpts.S3Bucket, "s3-bucket", "", "S3 bucket name")
	f.StringVar(&dockerOpts.S3Region, "s3-region", "", "S3 bucket region")
	f.StringVar(&dockerOpts.S3AccessKeyID, "s3-access-key", "", "S3 access key ID")
	f.StringVar(&dockerOpts.S3SecretAccessKey, "s3-secret-key", "", "S3 secret access key")
	f.StringVar(&dockerOpts.RedisURL, "redis-url", "", "Redis URL (active-active only)")

	_ = deployDockerCmd.MarkFlagRequired("name")
	_ = deployDockerCmd.MarkFlagRequired("hostname")
}
