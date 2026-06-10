// Phase 3: Azure Database for PostgreSQL Flexible Server provisioning.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PostgresConfig holds parameters for an Azure PostgreSQL Flexible Server.
type PostgresConfig struct {
	SubscriptionID string
	ResourceGroup  string
	ServerName     string
	Location       string
	AdminUser      string
	AdminPass      string
	SKUName        string // e.g. "Standard_D4s_v3"
	SKUTier        string // "GeneralPurpose"
	StorageGB      int
	Token          string
}

// EnsurePostgres creates (or verifies) an Azure PostgreSQL Flexible Server.
// Returns the postgres:// connection URL.
//
// Phase 3 implementation.
func EnsurePostgres(ctx context.Context, pcfg *PostgresConfig) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s?api-version=2023-06-01-preview",
		pcfg.SubscriptionID, pcfg.ResourceGroup, pcfg.ServerName,
	)

	skuName := pcfg.SKUName
	if skuName == "" {
		skuName = "Standard_D4s_v3"
	}
	skuTier := pcfg.SKUTier
	if skuTier == "" {
		skuTier = "GeneralPurpose"
	}
	storageGB := pcfg.StorageGB
	if storageGB == 0 {
		storageGB = 128
	}

	body := map[string]interface{}{
		"location": pcfg.Location,
		"sku": map[string]string{
			"name": skuName,
			"tier": skuTier,
		},
		"properties": map[string]interface{}{
			"administratorLogin":         pcfg.AdminUser,
			"administratorLoginPassword": pcfg.AdminPass,
			"version":                    "15",
			"storage":                    map[string]int{"storageSizeGB": storageGB},
			"backup": map[string]interface{}{
				"backupRetentionDays":    7,
				"geoRedundantBackup":     "Disabled",
			},
			"availabilityZone": "1",
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+pcfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating Azure PostgreSQL server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 202 {
		return "", fmt.Errorf("Azure PostgreSQL PUT returned HTTP %d", resp.StatusCode)
	}

	// Poll until provisioned.
	if err := waitPostgresReady(ctx, pcfg); err != nil {
		return "", err
	}

	host := fmt.Sprintf("%s.postgres.database.azure.com", pcfg.ServerName)
	return fmt.Sprintf("postgres://%s%%40%s:%s@%s:5432/tfe?sslmode=require",
		pcfg.AdminUser, pcfg.ServerName, pcfg.AdminPass, host), nil
}

func waitPostgresReady(ctx context.Context, pcfg *PostgresConfig) error {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s?api-version=2023-06-01-preview",
		pcfg.SubscriptionID, pcfg.ResourceGroup, pcfg.ServerName,
	)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+pcfg.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		var result struct {
			Properties struct {
				State string `json:"state"`
			} `json:"properties"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if result.Properties.State == "Ready" {
			return nil
		}
	}
}
