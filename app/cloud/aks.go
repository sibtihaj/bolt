package cloud

import (
	runner "github.com/sibtihaj/bolt/internal/exec"
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/state"
)

// ConfigureAKSKubeconfig runs az aks get-credentials to populate the kubeconfig.
func ConfigureAKSKubeconfig(d *state.TFEDeployment, creds *credentials.TFECredentials) error {
	args := []string{
		"aks", "get-credentials",
		"--resource-group", d.AKSResourceGroup,
		"--name", d.AKSClusterName,
		"--overwrite-existing",
	}
	if d.Kubeconfig != "" {
		args = append(args, "--file", d.Kubeconfig)
	}

	var env []string
	if creds.AzureClientID != "" {
		env = append(env,
			"AZURE_CLIENT_ID="+creds.AzureClientID,
			"AZURE_CLIENT_SECRET="+creds.AzureClientSecret,
			"AZURE_TENANT_ID="+creds.AzureTenantID,
		)
	}
	if creds.AzureSubscriptionID != "" {
		env = append(env, "AZURE_SUBSCRIPTION_ID="+creds.AzureSubscriptionID)
	}

	return runner.Run("az", args, runner.RunOptions{Env: env})
}
