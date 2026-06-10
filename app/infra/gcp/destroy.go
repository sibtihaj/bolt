// GCP teardown helpers for bolt destroy.
package gcp

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// DeleteCloudSQLInstance deletes a Cloud SQL instance.
func DeleteCloudSQLInstance(ctx context.Context, projectID, instanceName, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	url := fmt.Sprintf(
		"https://sqladmin.googleapis.com/sql/v1beta4/projects/%s/instances/%s",
		projectID, instanceName,
	)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting Cloud SQL instance: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 202 || resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("Cloud SQL deletion returned HTTP %d", resp.StatusCode)
}

// DeleteGKECluster deletes a GKE cluster.
func DeleteGKECluster(ctx context.Context, projectID, zone, clusterName, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	url := fmt.Sprintf(
		"https://container.googleapis.com/v1/projects/%s/zones/%s/clusters/%s",
		projectID, zone, clusterName,
	)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting GKE cluster: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 202 || resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("GKE cluster deletion returned HTTP %d", resp.StatusCode)
}
