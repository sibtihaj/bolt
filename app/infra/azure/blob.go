// Package azure provides Azure infrastructure provisioning for bolt.
// Phase 2: Azure Blob Storage provisioning.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// BlobConfig holds parameters for provisioning an Azure Blob Storage account + container.
type BlobConfig struct {
	SubscriptionID  string
	ResourceGroup   string
	StorageAccount  string // 3–24 chars, lowercase alphanumeric
	ContainerName   string
	Location        string // e.g. "eastus"
	Token           string // bearer token from preflight.GetAzureToken
}

// EnsureBlob creates (or verifies) the storage account and container.
// Returns the S3-compatible endpoint for use with TFE object storage.
func EnsureBlob(ctx context.Context, bcfg *BlobConfig) (endpoint, accessKey string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	if err := ensureResourceGroup(ctx, bcfg); err != nil {
		return "", "", err
	}

	if err := ensureStorageAccount(ctx, bcfg); err != nil {
		return "", "", err
	}

	if err := ensureContainer(ctx, bcfg); err != nil {
		return "", "", err
	}

	key, err := getStorageAccountKey(ctx, bcfg)
	if err != nil {
		return "", "", err
	}

	// Azure Blob Storage endpoint for S3-compatible access via MinIO gateway or
	// the native Azure provider in TFE.
	ep := fmt.Sprintf("https://%s.blob.core.windows.net", bcfg.StorageAccount)
	return ep, key, nil
}

func ensureResourceGroup(ctx context.Context, bcfg *BlobConfig) error {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourcegroups/%s?api-version=2021-04-01",
		bcfg.SubscriptionID, bcfg.ResourceGroup,
	)
	body := map[string]interface{}{"location": bcfg.Location}
	return azurePUT(ctx, url, bcfg.Token, body)
}

func ensureStorageAccount(ctx context.Context, bcfg *BlobConfig) error {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s?api-version=2023-01-01",
		bcfg.SubscriptionID, bcfg.ResourceGroup, bcfg.StorageAccount,
	)
	body := map[string]interface{}{
		"sku":      map[string]string{"name": "Standard_LRS"},
		"kind":     "StorageV2",
		"location": bcfg.Location,
		"properties": map[string]interface{}{
			"accessTier":              "Hot",
			"allowBlobPublicAccess":   false,
			"minimumTlsVersion":       "TLS1_2",
			"supportsHttpsTrafficOnly": true,
		},
	}
	return azurePUT(ctx, url, bcfg.Token, body)
}

func ensureContainer(ctx context.Context, bcfg *BlobConfig) error {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s/blobServices/default/containers/%s?api-version=2023-01-01",
		bcfg.SubscriptionID, bcfg.ResourceGroup, bcfg.StorageAccount, bcfg.ContainerName,
	)
	body := map[string]interface{}{
		"properties": map[string]string{"publicAccess": "None"},
	}
	return azurePUT(ctx, url, bcfg.Token, body)
}

func getStorageAccountKey(ctx context.Context, bcfg *BlobConfig) (string, error) {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s/listKeys?api-version=2023-01-01",
		bcfg.SubscriptionID, bcfg.ResourceGroup, bcfg.StorageAccount,
	)
	data, err := json.Marshal(map[string]interface{}{})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+bcfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("listing storage account keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("listing storage account keys returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Keys []struct {
			Value string `json:"value"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Keys) == 0 {
		return "", fmt.Errorf("no storage account keys returned")
	}
	return result.Keys[0].Value, nil
}

func azurePUT(ctx context.Context, url, token string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 202 {
		return fmt.Errorf("Azure PUT %s returned HTTP %d", url, resp.StatusCode)
	}
	return nil
}
