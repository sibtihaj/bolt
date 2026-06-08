package credentials

import (
	"fmt"
	"os"

	"github.com/sibtihaj/bolt/app/config"
)

// Flags holds the raw values parsed from CLI flags before resolution.
type Flags struct {
	License             string
	LicensePath         string
	EncryptionPassword  string
	TLSCertPath         string
	TLSKeyPath          string
	DatabaseURL         string
	S3Bucket            string
	S3Region            string
	S3AccessKeyID       string
	S3SecretAccessKey   string
	RedisURL            string
	// Cloud provider
	AWSProfile          string
	GCPSAKeyPath        string
	AzureClientID       string
	AzureClientSecret   string
	AzureTenantID       string
	AzureSubscriptionID string
}

// TFECredentials holds all resolved secrets. Nothing in here is written to
// disk — it lives only in memory for the duration of a command.
type TFECredentials struct {
	License            string
	EncryptionPassword string
	TLSCert            string // PEM file path (resolved)
	TLSKey             string // PEM file path (resolved)
	DatabaseURL        string
	S3Bucket           string
	S3Region           string
	S3AccessKeyID      string
	S3SecretAccessKey  string
	RedisURL           string
	// Cloud provider (used to configure kubeconfig)
	AWSProfile          string
	GCPSAKeyPath        string
	AzureClientID       string
	AzureClientSecret   string
	AzureTenantID       string
	AzureSubscriptionID string
}

// Resolve applies the priority chain: CLI flag → env var → config file.
// Required fields (License, EncryptionPassword) return an error if absent.
func Resolve(f Flags, cfg *config.TFEConfig) (*TFECredentials, error) {
	c := &TFECredentials{}

	// License: flag (raw string) > flag (path) > env TFE_LICENSE > env TFE_LICENSE_PATH > config path
	c.License = first(f.License, os.Getenv("TFE_LICENSE"))
	if c.License == "" {
		licensePath := first(f.LicensePath, os.Getenv("TFE_LICENSE_PATH"), cfg.DefaultLicensePath)
		if licensePath != "" {
			data, err := os.ReadFile(licensePath)
			if err != nil {
				return nil, fmt.Errorf("reading license file %s: %w", licensePath, err)
			}
			c.License = string(data)
		}
	}
	if c.License == "" {
		return nil, fmt.Errorf("TFE license is required: set --license, --license-path, TFE_LICENSE, or TFE_LICENSE_PATH")
	}

	// Encryption password
	c.EncryptionPassword = first(f.EncryptionPassword, os.Getenv("TFE_ENCRYPTION_PASSWORD"), cfg.DefaultEncryptionPass)
	if c.EncryptionPassword == "" {
		return nil, fmt.Errorf("encryption password is required: set --encryption-password or TFE_ENCRYPTION_PASSWORD")
	}

	// TLS paths (not contents — paths are passed through to tools)
	c.TLSCert = first(f.TLSCertPath, os.Getenv("TFE_TLS_CERT_FILE"))
	c.TLSKey = first(f.TLSKeyPath, os.Getenv("TFE_TLS_KEY_FILE"))

	// Storage
	c.DatabaseURL = first(f.DatabaseURL, os.Getenv("TFE_DATABASE_URL"))
	c.S3Bucket = first(f.S3Bucket, os.Getenv("TFE_S3_BUCKET"))
	c.S3Region = first(f.S3Region, os.Getenv("TFE_S3_REGION"))
	c.S3AccessKeyID = first(f.S3AccessKeyID, os.Getenv("TFE_S3_ACCESS_KEY_ID"))
	c.S3SecretAccessKey = first(f.S3SecretAccessKey, os.Getenv("TFE_S3_SECRET_ACCESS_KEY"))
	c.RedisURL = first(f.RedisURL, os.Getenv("TFE_REDIS_URL"))

	// Cloud provider
	c.AWSProfile = first(f.AWSProfile, os.Getenv("AWS_PROFILE"))
	c.GCPSAKeyPath = first(f.GCPSAKeyPath, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	c.AzureClientID = first(f.AzureClientID, os.Getenv("AZURE_CLIENT_ID"))
	c.AzureClientSecret = first(f.AzureClientSecret, os.Getenv("AZURE_CLIENT_SECRET"))
	c.AzureTenantID = first(f.AzureTenantID, os.Getenv("AZURE_TENANT_ID"))
	c.AzureSubscriptionID = first(f.AzureSubscriptionID, os.Getenv("AZURE_SUBSCRIPTION_ID"))

	return c, nil
}

// first returns the first non-empty string from the list.
func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
