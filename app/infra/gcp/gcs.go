// Package gcp provides GCP infrastructure provisioning for bolt.
// Phase 2: Google Cloud Storage bucket provisioning.
package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// GCSConfig holds parameters for provisioning a GCS bucket.
type GCSConfig struct {
	ProjectID  string
	BucketName string
	Location   string // e.g. "US", "EU", "us-central1"
	Token      string // bearer token from preflight.GetGCPToken
}

// EnsureGCSBucket creates (or verifies) a GCS bucket with uniform access
// and versioning enabled.  Returns the bucket name.
func EnsureGCSBucket(ctx context.Context, gcfg *GCSConfig) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Check whether the bucket already exists.
	checkURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s", gcfg.BucketName)
	req, _ := http.NewRequestWithContext(ctx, "GET", checkURL, nil)
	req.Header.Set("Authorization", "Bearer "+gcfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking GCS bucket: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		return gcfg.BucketName, nil // already exists
	}

	location := gcfg.Location
	if location == "" {
		location = "US"
	}

	// Create bucket.
	body := map[string]interface{}{
		"name":     gcfg.BucketName,
		"location": location,
		"storageClass": "STANDARD",
		"iamConfiguration": map[string]interface{}{
			"uniformBucketLevelAccess": map[string]bool{"enabled": true},
		},
		"versioning": map[string]bool{"enabled": true},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	createURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b?project=%s", gcfg.ProjectID)
	req, err = http.NewRequestWithContext(ctx, "POST", createURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+gcfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating GCS bucket %q: %w", gcfg.BucketName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var result struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		return "", fmt.Errorf("GCS bucket creation returned HTTP %d: %s", resp.StatusCode, result.Error.Message)
	}

	return gcfg.BucketName, nil
}

// DeleteGCSBucket deletes the bucket and all its objects.
// Safe to call if the bucket does not exist.
func DeleteGCSBucket(ctx context.Context, gcfg *GCSConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// List and delete all objects first.
	if err := deleteAllGCSObjects(ctx, gcfg); err != nil {
		return err
	}

	deleteURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s", gcfg.BucketName)
	req, _ := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	req.Header.Set("Authorization", "Bearer "+gcfg.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("deleting GCS bucket: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("GCS bucket deletion returned HTTP %d", resp.StatusCode)
}

func deleteAllGCSObjects(ctx context.Context, gcfg *GCSConfig) error {
	listURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o", gcfg.BucketName)
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", listURL, nil)
		req.Header.Set("Authorization", "Bearer "+gcfg.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		var page struct {
			Items         []struct{ Name string `json:"name"` } `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()

		for _, obj := range page.Items {
			delURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o/%s",
				gcfg.BucketName, obj.Name)
			r, _ := http.NewRequestWithContext(ctx, "DELETE", delURL, nil)
			r.Header.Set("Authorization", "Bearer "+gcfg.Token)
			dr, err := http.DefaultClient.Do(r)
			if err != nil {
				return err
			}
			dr.Body.Close()
		}

		if page.NextPageToken == "" {
			return nil
		}
		listURL = fmt.Sprintf(
			"https://storage.googleapis.com/storage/v1/b/%s/o?pageToken=%s",
			gcfg.BucketName, page.NextPageToken,
		)
	}
}
