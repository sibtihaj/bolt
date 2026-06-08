package config

// TFEConfig is loaded from ~/.bolt/config.yaml and provides defaults that
// the credential resolver checks when flags and env vars are both absent.
type TFEConfig struct {
	DefaultLicensePath    string `yaml:"default_license_path,omitempty"`
	DefaultEncryptionPass string `yaml:"default_encryption_password,omitempty"`
	DefaultImageTag       string `yaml:"default_image_tag,omitempty"`
	// Arbitrary key/value store for any extra defaults users want to set.
	Defaults map[string]string `yaml:"defaults,omitempty"`
}
