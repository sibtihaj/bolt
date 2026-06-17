package helm

import (
	_ "embed"
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/sibtihaj/bolt/app/state"
)

//go:embed templates/values-disk.yaml.tmpl
var diskTmpl string

//go:embed templates/values-external.yaml.tmpl
var externalTmpl string

//go:embed templates/values-active-active.yaml.tmpl
var activeActiveTmpl string

type valuesData struct {
	Hostname     string
	ImageTag     string
	ReplicaCount int
	LocalDeploy  bool // true for kind/kubeadm — sets reduced resource requests
}

// BuildValues renders the Helm values.yaml for the given deployment mode.
func BuildValues(d *state.TFEDeployment) (string, error) {
	var raw string
	switch d.Mode {
	case state.ModeDisk:
		raw = diskTmpl
	case state.ModeExternal:
		raw = externalTmpl
	case state.ModeActiveActive:
		raw = activeActiveTmpl
	default:
		return "", fmt.Errorf("unknown operational mode: %s", d.Mode)
	}

	tmpl, err := template.New("values").Parse(raw)
	if err != nil {
		return "", err
	}

	replicaCount := 1
	if d.Mode == state.ModeActiveActive {
		replicaCount = 2
	}

	hostname := d.Hostname
	hostname = strings.TrimSpace(hostname)
	hostname = strings.TrimPrefix(hostname, "https://")
	hostname = strings.TrimPrefix(hostname, "http://")
	hostname = strings.TrimRight(hostname, "/")

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, valuesData{
		Hostname:     hostname,
		ImageTag:     d.ImageTag,
		ReplicaCount: replicaCount,
		LocalDeploy:  d.ClusterType == state.ClusterKind || d.ClusterType == state.ClusterKubeadm,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
