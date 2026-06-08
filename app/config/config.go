package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultPath returns ~/.bolt/config.yaml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/.bolt/config.yaml", nil
}

// Load reads the config file at path. Returns an empty config (not an error)
// if the file does not exist — it is optional.
func Load(path string) (*TFEConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &TFEConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg TFEConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes cfg to path, creating the file if needed.
func Save(path string, cfg *TFEConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
