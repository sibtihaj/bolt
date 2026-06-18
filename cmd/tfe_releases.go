package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"time"
)

var versionTagRE = regexp.MustCompile(`^v\d{6}-\d+$`)

// fetchTFEReleaseTags queries the container registry and returns stable release
// tags (v2YYYYMM-N format) sorted newest-first.  Returns an error when
// credentials are missing or the registry is unreachable.
func fetchTFEReleaseTags(username, password string) ([]string, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("registry credentials not available")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet,
		"https://images.releases.hashicorp.com/v2/hashicorp/terraform-enterprise/tags/list", nil)
	if err != nil {
		return nil, err
	}
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	req.Header.Set("Authorization", "Basic "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach registry: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse registry response: %w", err)
	}

	var stable []string
	for _, t := range payload.Tags {
		if versionTagRE.MatchString(t) {
			stable = append(stable, t)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(stable)))
	return stable, nil
}
