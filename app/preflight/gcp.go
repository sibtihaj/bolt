package preflight

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GCPConfig holds credential material for GCP.  Never persisted to disk.
type GCPConfig struct {
	ProjectID       string
	Region          string
	Zone            string
	ServiceAcctJSON string // absolute path to a service-account key JSON file
}

// ValidateGCPCredentials reads the service-account key file, obtains an
// access token via JWT grant, and confirms the project is accessible.
func ValidateGCPCredentials(cfg *GCPConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	token, err := GetGCPToken(ctx, cfg.ServiceAcctJSON)
	if err != nil {
		return fmt.Errorf("GCP authentication failed: %w", err)
	}

	reqURL := fmt.Sprintf(
		"https://cloudresourcemanager.googleapis.com/v1/projects/%s",
		cfg.ProjectID,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GCP project lookup failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		return nil
	case 401, 403:
		return fmt.Errorf("GCP credentials lack access to project %q (HTTP %d)",
			cfg.ProjectID, resp.StatusCode)
	default:
		return fmt.Errorf("GCP project %q not found or inaccessible (HTTP %d)",
			cfg.ProjectID, resp.StatusCode)
	}
}

// GetGCPToken returns a short-lived access token from a service-account key file.
// Falls back to GOOGLE_APPLICATION_CREDENTIALS if keyFilePath is empty.
func GetGCPToken(ctx context.Context, keyFilePath string) (string, error) {
	if keyFilePath == "" {
		keyFilePath = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	}
	if keyFilePath == "" {
		return "", fmt.Errorf("service account key path required " +
			"(or set GOOGLE_APPLICATION_CREDENTIALS)")
	}

	data, err := os.ReadFile(keyFilePath)
	if err != nil {
		return "", fmt.Errorf("reading service account key: %w", err)
	}

	var sa struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(data, &sa); err != nil {
		return "", fmt.Errorf("parsing service account key: %w", err)
	}
	if sa.Type != "service_account" {
		return "", fmt.Errorf("expected service_account key file, got type %q", sa.Type)
	}
	if sa.PrivateKey == "" || sa.ClientEmail == "" {
		return "", fmt.Errorf("service account key file is missing private_key or client_email")
	}

	return gcpJWTGrant(ctx, sa.ClientEmail, sa.PrivateKey, sa.TokenURI)
}

// gcpJWTGrant creates a signed JWT and exchanges it for an access token.
func gcpJWTGrant(ctx context.Context, clientEmail, privateKeyPEM, tokenURI string) (string, error) {
	key, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return "", fmt.Errorf("parsing RSA private key: %w", err)
	}

	now := time.Now().Unix()
	headerJSON := `{"alg":"RS256","typ":"JWT"}`
	claimsJSON := fmt.Sprintf(
		`{"iss":%q,"scope":"https://www.googleapis.com/auth/cloud-platform","aud":%q,"iat":%d,"exp":%d}`,
		clientEmail, tokenURI, now, now+3600,
	)

	header := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	claims := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	sigInput := header + "." + claims

	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	jwt := sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	resp, err := http.PostForm(tokenURI, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	})
	if err != nil {
		return "", fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing GCP token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s: %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token returned from GCP")
	}
	return result.AccessToken, nil
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key")
	}
	// Try PKCS8 first (common for GCP service accounts), then PKCS1.
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return rsaKey, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
