package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"
)

var (
	yearlyTagRE = regexp.MustCompile(`^v(\d{4})(\d{2})-(\d+)$`) // v202507-1
	legacyTagRE = regexp.MustCompile(`^[12]\.\d+\.\d+$`)        // 1.2.1, 2.0.2
)

var monthNames = [...]string{
	"", "January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

// releaseGroup holds the releases for one selectable group (a year, or legacy).
type releaseGroup struct {
	key      string   // "2025", "2024", "legacy"
	label    string   // shown in the year picker
	releases []string // version tags, newest-first
}

// fetchTFEReleaseTags queries the container registry and returns all stable
// release tags (yearly and legacy semantic versions), newest-first.
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
		if yearlyTagRE.MatchString(t) || legacyTagRE.MatchString(t) {
			stable = append(stable, t)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(stable)))
	return stable, nil
}

// groupReleases partitions tags into per-year groups (descending) followed by
// a legacy group for semantic-version tags (1.x / 2.x).
func groupReleases(tags []string) []releaseGroup {
	yearMap := map[string][]string{}
	var legacy []string

	for _, t := range tags {
		if m := yearlyTagRE.FindStringSubmatch(t); m != nil {
			year := m[1] // "2025"
			yearMap[year] = append(yearMap[year], t)
		} else if legacyTagRE.MatchString(t) {
			legacy = append(legacy, t)
		}
	}

	years := make([]string, 0, len(yearMap))
	for y := range yearMap {
		years = append(years, y)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(years)))

	var groups []releaseGroup
	for _, y := range years {
		releases := yearMap[y]
		sort.Sort(sort.Reverse(sort.StringSlice(releases)))
		n := len(releases)
		noun := "release"
		if n != 1 {
			noun = "releases"
		}
		groups = append(groups, releaseGroup{
			key:      y,
			label:    fmt.Sprintf("%s  (%d %s)", y, n, noun),
			releases: releases,
		})
	}

	if len(legacy) > 0 {
		sort.Sort(sort.Reverse(sort.StringSlice(legacy)))
		groups = append(groups, releaseGroup{
			key:      "legacy",
			label:    fmt.Sprintf("1.x / 2.x  (%d older releases)", len(legacy)),
			releases: legacy,
		})
	}

	return groups
}

// releaseLabel returns a human-friendly display string for a version tag.
//
//	v202507-1  →  "v202507-1  —  July 2025"
//	1.2.1      →  "1.2.1"
func releaseLabel(tag string) string {
	if m := yearlyTagRE.FindStringSubmatch(tag); m != nil {
		month, _ := strconv.Atoi(m[2])
		patch := m[3]
		suffix := ""
		if patch != "1" {
			suffix = fmt.Sprintf("  (patch %s)", patch)
		}
		if month >= 1 && month <= 12 {
			return fmt.Sprintf("%-14s  —  %s %s%s", tag, monthNames[month], m[1], suffix)
		}
	}
	return tag
}
