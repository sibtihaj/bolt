// Package preflight — Doormat CLI integration for short-lived AWS credentials.
package preflight

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DoormatCreds mirrors the JSON payload returned by `doormat aws json`.
type DoormatCreds struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

// DoormatAvailable returns true if the doormat CLI is present in PATH.
func DoormatAvailable() bool {
	_, err := exec.LookPath("doormat")
	return err == nil
}

// DoormatSessionValid returns true if the current doormat session is still
// active. It does this by running `doormat aws list` — if the session is
// expired the command exits non-zero.
func DoormatSessionValid() bool {
	cmd := exec.Command("doormat", "aws", "list")
	return cmd.Run() == nil
}

// DoormatLogin runs `doormat login`, streaming output live to the terminal
// so the user can see the browser authentication URL and follow the flow.
func DoormatLogin() error {
	cmd := exec.Command("doormat", "login")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("doormat login failed: %w", err)
	}
	return nil
}

// DoormatListRoles runs `doormat aws list` and returns the eligible IAM role ARNs.
func DoormatListRoles() ([]string, error) {
	out, err := exec.Command("doormat", "aws", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("doormat aws list: %w", err)
	}

	var roles []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "arn:aws:iam::") {
			roles = append(roles, line)
		}
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("no eligible roles found — check your doormat account access")
	}
	return roles, nil
}

// DoormatGetCredentials runs `doormat aws json --role <arn>` and returns an
// AWSConfig populated with short-lived credentials plus the raw creds struct
// (for displaying expiry to the user).
func DoormatGetCredentials(roleARN, region string) (*AWSConfig, *DoormatCreds, error) {
	out, err := exec.Command("doormat", "aws", "json", "--role", roleARN).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("doormat aws json: %w", err)
	}

	var d DoormatCreds
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, nil, fmt.Errorf("parsing doormat credentials: %w", err)
	}

	cfg := &AWSConfig{
		AccessKeyID:     d.AccessKeyID,
		SecretAccessKey: d.SecretAccessKey,
		SessionToken:    d.SessionToken,
		Region:          region,
	}
	return cfg, &d, nil
}

// DoormatRoleLabel returns a human-friendly label for a role ARN.
// "arn:aws:iam::861276082219:role/aws_syed.haque_test-developer"
// → "aws_syed.haque_test-developer  (861276082219)"
func DoormatRoleLabel(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return arn
	}
	accountID := parts[4]
	roleName := strings.TrimPrefix(parts[5], "role/")
	return fmt.Sprintf("%s  (%s)", roleName, accountID)
}

// DoormatExpiresIn returns a human-friendly remaining-lifetime string.
// e.g. "expires in 59m"
func DoormatExpiresIn(expiration string) string {
	t, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		return ""
	}
	remaining := time.Until(t).Round(time.Minute)
	if remaining <= 0 {
		return "already expired"
	}
	if remaining >= time.Hour {
		h := int(remaining.Hours())
		m := int(remaining.Minutes()) % 60
		return fmt.Sprintf("expires in %dh %dm", h, m)
	}
	return fmt.Sprintf("expires in %dm", int(remaining.Minutes()))
}
