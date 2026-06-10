// Phase 3: Cloud SQL PostgreSQL provisioning.
package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CloudSQLConfig holds parameters for a Cloud SQL PostgreSQL instance.
type CloudSQLConfig struct {
	ProjectID    string
	InstanceName string // e.g. "bolt-prod-db"
	Region       string // e.g. "us-central1"
	Tier         string // e.g. "db-custom-4-16384"
	StorageGB    int
	DBName       string
	UserName     string
	UserPass     string
	Token        string
}

// EnsureCloudSQL creates (or verifies) a Cloud SQL PostgreSQL 15 instance.
// Returns the postgres:// connection URL.
//
// Phase 3 implementation.
func EnsureCloudSQL(ctx context.Context, cfg *CloudSQLConfig) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// Check existence.
	existing, err := describeCloudSQL(ctx, cfg)
	if err == nil && existing == "RUNNABLE" {
		return cloudSQLURL(cfg), nil
	}

	tier := cfg.Tier
	if tier == "" {
		tier = "db-custom-4-16384"
	}
	storageGB := cfg.StorageGB
	if storageGB == 0 {
		storageGB = 100
	}

	body := map[string]interface{}{
		"name":            cfg.InstanceName,
		"region":          cfg.Region,
		"databaseVersion": "POSTGRES_15",
		"settings": map[string]interface{}{
			"tier":           tier,
			"dataDiskSizeGb": fmt.Sprintf("%d", storageGB),
			"dataDiskType":   "PD_SSD",
			"backupConfiguration": map[string]interface{}{
				"enabled":                    true,
				"binaryLogEnabled":           false,
				"transactionLogRetentionDays": 7,
			},
			"ipConfiguration": map[string]interface{}{
				"ipv4Enabled":    true,
				"requireSsl":     true,
			},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf(
		"https://sqladmin.googleapis.com/v1/projects/%s/instances",
		cfg.ProjectID,
	)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating Cloud SQL instance: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var result struct {
			Error struct{ Message string `json:"message"` } `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		return "", fmt.Errorf("Cloud SQL creation returned HTTP %d: %s", resp.StatusCode, result.Error.Message)
	}

	if err := waitCloudSQLRunnable(ctx, cfg); err != nil {
		return "", err
	}

	if err := createCloudSQLUser(ctx, cfg); err != nil {
		return "", err
	}

	return cloudSQLURL(cfg), nil
}

func describeCloudSQL(ctx context.Context, cfg *CloudSQLConfig) (string, error) {
	url := fmt.Sprintf(
		"https://sqladmin.googleapis.com/v1/projects/%s/instances/%s",
		cfg.ProjectID, cfg.InstanceName,
	)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var result struct {
		State string `json:"state"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.State, nil
}

func waitCloudSQLRunnable(ctx context.Context, cfg *CloudSQLConfig) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
		state, err := describeCloudSQL(ctx, cfg)
		if err == nil && state == "RUNNABLE" {
			return nil
		}
	}
}

func createCloudSQLUser(ctx context.Context, cfg *CloudSQLConfig) error {
	url := fmt.Sprintf(
		"https://sqladmin.googleapis.com/v1/projects/%s/instances/%s/users",
		cfg.ProjectID, cfg.InstanceName,
	)
	body := map[string]string{
		"name":     cfg.UserName,
		"password": cfg.UserPass,
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("creating Cloud SQL user: %w", err)
	}
	resp.Body.Close()
	return nil
}

func cloudSQLURL(cfg *CloudSQLConfig) string {
	return fmt.Sprintf(
		"postgres://%s:%s@/%s?host=/cloudsql/%s:%s:%s&sslmode=disable",
		cfg.UserName, cfg.UserPass, cfg.DBName,
		cfg.ProjectID, cfg.Region, cfg.InstanceName,
	)
}
