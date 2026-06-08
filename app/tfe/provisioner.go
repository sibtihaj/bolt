package tfe

import (
	"fmt"

	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/state"
)

// Provisioner is the common interface for all deployment backends.
type Provisioner interface {
	Deploy(creds *credentials.TFECredentials) error
	Destroy(force bool) error
	Status() (*ProvisionerStatus, error)
}

// ProvisionerStatus is the result of a Status() call.
type ProvisionerStatus struct {
	DeploymentStatus state.DeploymentStatus
	URL              string
	Message          string
}

// NewProvisioner returns the correct provisioner for the deployment's backend.
func NewProvisioner(d *state.TFEDeployment) (Provisioner, error) {
	switch d.Backend {
	case state.BackendK8s:
		return &K8sProvisioner{deployment: d}, nil
	case state.BackendDocker:
		return &DockerProvisioner{deployment: d}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q", d.Backend)
	}
}
