package preflight

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidateResourceName checks a cloud resource name against provider-specific
// rules. Returns nil if valid; returns an error with a sanitized suggestion otherwise.
func ValidateResourceName(name, resourceType, cloud string) error {
	rule := findRule(cloud, resourceType)
	if rule == nil {
		return nil
	}
	return rule.validate(name)
}

// SanitizeName rewrites name to the nearest valid form for the given cloud
// resource type. Returns the original name if no rule is defined.
func SanitizeName(name, resourceType, cloud string) string {
	rule := findRule(cloud, resourceType)
	if rule == nil {
		return name
	}
	return rule.sanitize(name)
}

func findRule(cloud, resourceType string) *namingRule {
	cloudRules, ok := rules[cloud]
	if !ok {
		return nil
	}
	r, ok := cloudRules[resourceType]
	if !ok {
		r = cloudRules["default"]
	}
	return r
}

type namingRule struct {
	minLen      int
	maxLen      int
	pattern     *regexp.Regexp
	description string
	sanitizeFn  func(string) string
}

func (r *namingRule) validate(name string) error {
	if len(name) < r.minLen || len(name) > r.maxLen {
		return fmt.Errorf("%q length %d is outside %d–%d chars. Suggestion: %q",
			name, len(name), r.minLen, r.maxLen, r.sanitize(name))
	}
	if !r.pattern.MatchString(name) {
		return fmt.Errorf("%q does not match %s naming rule. Suggestion: %q",
			name, r.description, r.sanitize(name))
	}
	return nil
}

func (r *namingRule) sanitize(name string) string {
	result := r.sanitizeFn(name)
	if len(result) > r.maxLen {
		result = result[:r.maxLen]
	}
	result = strings.Trim(result, "-_.")
	if len(result) < r.minLen {
		result = "bolt-" + result
	}
	return result
}

var (
	// AWS S3: 3-63 chars, lowercase alphanumeric + hyphens/dots, no uppercase
	awsS3Rule = &namingRule{
		minLen:      3,
		maxLen:      63,
		pattern:     regexp.MustCompile(`^[a-z0-9][a-z0-9\-\.]{1,61}[a-z0-9]$`),
		description: "S3 bucket (3–63 chars, lowercase alphanumeric + hyphens/dots)",
		sanitizeFn: func(s string) string {
			s = strings.ToLower(s)
			s = regexp.MustCompile(`[^a-z0-9\-\.]`).ReplaceAllString(s, "-")
			s = regexp.MustCompile(`\.{2,}|-{2,}`).ReplaceAllString(s, "-")
			s = strings.Trim(s, "-.")
			return s
		},
	}

	// AWS EKS / general: 1–100 chars, alphanumeric + hyphens/underscores, start with letter
	awsGeneralRule = &namingRule{
		minLen:      1,
		maxLen:      100,
		pattern:     regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9\-_]{0,99}$`),
		description: "AWS resource (start with letter, alphanumeric + hyphens/underscores)",
		sanitizeFn: func(s string) string {
			s = regexp.MustCompile(`[^a-zA-Z0-9\-_]`).ReplaceAllString(s, "-")
			if len(s) > 0 && !regexp.MustCompile(`^[a-zA-Z]`).MatchString(s) {
				s = "bolt-" + s
			}
			return s
		},
	}

	// Azure general (RG, AKS, postgres): 1–90 chars, alphanumeric + hyphens/underscores/dots
	azureGeneralRule = &namingRule{
		minLen:      1,
		maxLen:      90,
		pattern:     regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-_\.]{0,88}[a-zA-Z0-9]$`),
		description: "Azure resource (1–90 chars, alphanumeric + hyphens/underscores/dots)",
		sanitizeFn: func(s string) string {
			s = regexp.MustCompile(`[^a-zA-Z0-9\-_\.]`).ReplaceAllString(s, "-")
			s = strings.Trim(s, "-._")
			return s
		},
	}

	// Azure Blob storage account: 3–24 chars, lowercase letters and numbers ONLY (no hyphens)
	azureStorageRule = &namingRule{
		minLen:      3,
		maxLen:      24,
		pattern:     regexp.MustCompile(`^[a-z0-9]{3,24}$`),
		description: "Azure storage account (3–24 chars, lowercase alphanumeric only, no hyphens)",
		sanitizeFn: func(s string) string {
			s = strings.ToLower(s)
			s = regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(s, "")
			return s
		},
	}

	// GCS bucket: 3–63 chars, lowercase alphanumeric + hyphens/underscores/dots
	gcsRule = &namingRule{
		minLen:      3,
		maxLen:      63,
		pattern:     regexp.MustCompile(`^[a-z0-9][a-z0-9\-_\.]{1,61}[a-z0-9]$`),
		description: "GCS bucket (3–63 chars, lowercase alphanumeric + hyphens/underscores/dots)",
		sanitizeFn: func(s string) string {
			s = strings.ToLower(s)
			s = regexp.MustCompile(`[^a-z0-9\-_\.]`).ReplaceAllString(s, "-")
			s = strings.Trim(s, "-.")
			return s
		},
	}

	// GCP general (GKE, Cloud SQL): 1–63 chars, lowercase alphanumeric + hyphens
	gcpGeneralRule = &namingRule{
		minLen:      1,
		maxLen:      63,
		pattern:     regexp.MustCompile(`^[a-z][a-z0-9\-]{0,62}$`),
		description: "GCP resource (1–63 chars, lowercase alphanumeric + hyphens, start with letter)",
		sanitizeFn: func(s string) string {
			s = strings.ToLower(s)
			s = regexp.MustCompile(`[^a-z0-9\-]`).ReplaceAllString(s, "-")
			if len(s) > 0 && !regexp.MustCompile(`^[a-z]`).MatchString(s) {
				s = "bolt-" + s
			}
			return s
		},
	}

	rules = map[string]map[string]*namingRule{
		"aws": {
			"s3":      awsS3Rule,
			"eks":     awsGeneralRule,
			"rds":     awsGeneralRule,
			"vpc":     awsGeneralRule,
			"iam":     awsGeneralRule,
			"default": awsGeneralRule,
		},
		"azure": {
			"storage":  azureStorageRule,
			"aks":      azureGeneralRule,
			"rg":       azureGeneralRule,
			"postgres": azureGeneralRule,
			"default":  azureGeneralRule,
		},
		"gcp": {
			"gcs":      gcsRule,
			"gke":      gcpGeneralRule,
			"cloudsql": gcpGeneralRule,
			"default":  gcpGeneralRule,
		},
	}
)
