package cloud

import (
	runner "github.com/sibtihaj/bolt/internal/exec"
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/state"
)

// ConfigureGKEKubeconfig runs gcloud container clusters get-credentials to
// populate the kubeconfig.
func ConfigureGKEKubeconfig(d *state.TFEDeployment, creds *credentials.TFECredentials) error {
	args := []string{
		"container", "clusters", "get-credentials",
		d.GKEClusterName,
		"--zone", d.GKEZone,
		"--project", d.GKEProject,
	}

	var env []string
	if creds.GCPSAKeyPath != "" {
		env = append(env, "GOOGLE_APPLICATION_CREDENTIALS="+creds.GCPSAKeyPath)
	}
	if d.Kubeconfig != "" {
		env = append(env, "KUBECONFIG="+d.Kubeconfig)
	}

	return runner.Run("gcloud", args, runner.RunOptions{Env: env})
}
