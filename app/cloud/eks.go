package cloud

import (
	runner "github.com/sibtihaj/bolt/internal/exec"
	"github.com/sibtihaj/bolt/app/state"
)

// ConfigureEKSKubeconfig runs aws eks update-kubeconfig to populate the
// kubeconfig with credentials for the given EKS cluster.
func ConfigureEKSKubeconfig(d *state.TFEDeployment, awsProfile string) error {
	args := []string{
		"eks", "update-kubeconfig",
		"--name", d.EKSClusterName,
		"--region", d.EKSRegion,
	}
	if d.Kubeconfig != "" {
		args = append(args, "--kubeconfig", d.Kubeconfig)
	}
	var env []string
	if awsProfile != "" {
		env = append(env, "AWS_PROFILE="+awsProfile)
	}
	return runner.Run("aws", args, runner.RunOptions{Env: env})
}
