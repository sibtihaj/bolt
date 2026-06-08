package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns ~/.bolt/deployments, creating it on first call.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".bolt", "deployments")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// DataRoot returns ~/.bolt — the root of all bolt runtime state.
func DataRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".bolt")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return root, nil
}

func statePath(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// Load reads a deployment by name. Returns os.ErrNotExist if not found.
func Load(name string) (*TFEDeployment, error) {
	path, err := statePath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d TFEDeployment
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	return &d, nil
}

// Save atomically writes deployment state (write to .tmp, then rename).
func Save(d *TFEDeployment) error {
	path, err := statePath(d.Name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Delete removes a deployment's state file.
func Delete(name string) error {
	path, err := statePath(name)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// List returns all known deployments.
func List() ([]*TFEDeployment, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var deployments []*TFEDeployment
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		d, err := Load(name)
		if err != nil {
			continue
		}
		deployments = append(deployments, d)
	}
	return deployments, nil
}
