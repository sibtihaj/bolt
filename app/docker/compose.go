package docker

import (
	_ "embed"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/sibtihaj/bolt/app/state"
)

//go:embed templates/compose-disk.yaml.tmpl
var composeDiskTmpl string

//go:embed templates/compose-external.yaml.tmpl
var composeExternalTmpl string

//go:embed templates/compose-active-active.yaml.tmpl
var composeActiveActiveTmpl string

type composeData struct {
	Name        string
	Hostname    string
	ImageTag    string
	DataDir     string
	TLSCertPath string
	TLSKeyPath  string
}

// BuildCompose renders the docker-compose YAML for the given deployment.
// Secrets are left as ${VAR} placeholders — they are never embedded in the file.
func BuildCompose(d *state.TFEDeployment) (string, error) {
	var raw string
	switch d.Mode {
	case state.ModeDisk:
		raw = composeDiskTmpl
	case state.ModeExternal:
		raw = composeExternalTmpl
	case state.ModeActiveActive:
		raw = composeActiveActiveTmpl
	default:
		return "", fmt.Errorf("unknown operational mode: %s", d.Mode)
	}

	tmpl, err := template.New("compose").Parse(raw)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, composeData{
		Name:        d.Name,
		Hostname:    d.Hostname,
		ImageTag:    d.ImageTag,
		DataDir:     d.DataDir,
		TLSCertPath: d.TLSCertPath,
		TLSKeyPath:  d.TLSKeyPath,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// WriteCompose renders and writes the compose file to ~/.bolt/compose/<name>/docker-compose.yaml.
func WriteCompose(d *state.TFEDeployment) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".bolt", "compose", d.Name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	content, err := BuildCompose(d)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", err
	}
	return path, nil
}
