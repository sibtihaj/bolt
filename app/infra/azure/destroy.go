// Azure teardown helpers for bolt destroy.
package azure

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// DeleteStorageAccount deletes the storage account and container.
// Safe to call if the account no longer exists.
func DeleteStorageAccount(ctx context.Context, subscriptionID, resourceGroup, storageAccount, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s?api-version=2023-01-01",
		subscriptionID, resourceGroup, storageAccount,
	)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting Azure storage account: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 202 || resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("Azure storage account deletion returned HTTP %d", resp.StatusCode)
}

// DeletePostgresServer deletes an Azure DB for PostgreSQL Flexible Server.
func DeletePostgresServer(ctx context.Context, subscriptionID, resourceGroup, serverName, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s?api-version=2023-06-01-preview",
		subscriptionID, resourceGroup, serverName,
	)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting Azure PostgreSQL server: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 202 || resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("Azure PostgreSQL deletion returned HTTP %d", resp.StatusCode)
}

// DeleteAKSCluster deletes an AKS cluster.
func DeleteAKSCluster(ctx context.Context, subscriptionID, resourceGroup, clusterName, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s?api-version=2024-01-01",
		subscriptionID, resourceGroup, clusterName,
	)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting AKS cluster: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 202 || resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("AKS cluster deletion returned HTTP %d", resp.StatusCode)
}
