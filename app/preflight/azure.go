package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AzureConfig holds credential material for Azure.  Never persisted to disk.
type AzureConfig struct {
	SubscriptionID string
	TenantID       string
	ClientID       string
	ClientSecret   string
	ResourceGroup  string
	Location       string // e.g. "eastus"
}

// ValidateAzureCredentials authenticates via client credentials grant and
// verifies the subscription is accessible.
func ValidateAzureCredentials(cfg *AzureConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	token, err := azureClientCredentialsToken(ctx, cfg.TenantID, cfg.ClientID, cfg.ClientSecret)
	if err != nil {
		return fmt.Errorf("Azure authentication failed: %w", err)
	}

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s?api-version=2020-01-01",
		cfg.SubscriptionID,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Azure subscription lookup failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		return nil
	case 401, 403:
		return fmt.Errorf("Azure credentials are invalid or lack access to subscription %q (HTTP %d)",
			cfg.SubscriptionID, resp.StatusCode)
	default:
		return fmt.Errorf("Azure subscription %q not found or inaccessible (HTTP %d)",
			cfg.SubscriptionID, resp.StatusCode)
	}
}

// GetAzureToken returns a management-plane bearer token.  Used by infra provisioners.
func GetAzureToken(ctx context.Context, cfg *AzureConfig) (string, error) {
	return azureClientCredentialsToken(ctx, cfg.TenantID, cfg.ClientID, cfg.ClientSecret)
}

func azureClientCredentialsToken(ctx context.Context, tenantID, clientID, clientSecret string) (string, error) {
	url := fmt.Sprintf(
		"https://login.microsoftonline.com/%s/oauth2/v2.0/token",
		tenantID,
	)
	body := strings.NewReader(fmt.Sprintf(
		"client_id=%s&client_secret=%s&scope=https%%3A%%2F%%2Fmanagement.azure.com%%2F.default&grant_type=client_credentials",
		clientID, clientSecret,
	))

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s: %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token returned")
	}
	return result.AccessToken, nil
}
