package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const apiURL = "https://api.github.com/repos/sibtihaj/bolt/releases/latest"

// Release holds minimal info from the latest GitHub release.
type Release struct {
	Version string // without leading "v"
	URL     string // HTML URL to the release page
}

// Check fetches the latest GitHub release and returns a Release if the latest
// version is strictly newer than currentVersion. Returns nil on any error,
// when already up to date, or when BOLT_NO_UPDATE_CHECK=1 is set.
func Check(currentVersion string) *Release {
	if currentVersion == "dev" || os.Getenv("BOLT_NO_UPDATE_CHECK") == "1" {
		return nil
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil
	}

	latest := strings.TrimPrefix(payload.TagName, "v")
	if latest == "" || latest == currentVersion {
		return nil
	}
	if !isNewer(latest, currentVersion) {
		return nil
	}
	return &Release{Version: latest, URL: payload.HTMLURL}
}

// isNewer returns true if candidate is strictly newer than current (semver MAJOR.MINOR.PATCH).
func isNewer(candidate, current string) bool {
	ca := splitVer(candidate)
	cu := splitVer(current)
	for i := range ca {
		if ca[i] > cu[i] {
			return true
		}
		if ca[i] < cu[i] {
			return false
		}
	}
	return false
}

func splitVer(v string) [3]int {
	var parts [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		if i >= 3 {
			break
		}
		fmt.Sscanf(s, "%d", &parts[i])
	}
	return parts
}
