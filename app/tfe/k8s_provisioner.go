package tfe

import (
	"bytes"
	"fmt"
	"time"

	"github.com/sibtihaj/bolt/app/cloud"
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/diagnostics"
	"github.com/sibtihaj/bolt/app/helm"
	"github.com/sibtihaj/bolt/app/kubectl"
	"github.com/sibtihaj/bolt/app/retry"
	"github.com/sibtihaj/bolt/app/state"
)

type K8sProvisioner struct {
	deployment *state.TFEDeployment
}

func (p *K8sProvisioner) Deploy(creds *credentials.TFECredentials) error {
	d := p.deployment

	// 1. Prerequisite check
	fmt.Println("→ Checking prerequisites...")
	if err := kubectl.CheckPrereqs(); err != nil {
		return err
	}
	if err := helm.CheckPrereqs(); err != nil {
		return err
	}

	// 2. Cloud kubeconfig (skip if --kubeconfig was explicitly provided)
	if d.Kubeconfig == "" {
		fmt.Printf("→ Configuring kubeconfig for %s...\n", d.ClusterType)
		if err := retry.Do("configure kubeconfig", retry.DefaultCloud, func(_ *bytes.Buffer) error {
			return p.configureKubeconfig(creds)
		}); err != nil {
			return fmt.Errorf("kubeconfig setup: %w", err)
		}
	}

	// 3. Create namespace
	fmt.Printf("→ Creating namespace %q...\n", d.Namespace)
	if err := kubectl.CreateNamespace(d); err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}

	// 4. Create secrets
	fmt.Println("→ Creating Kubernetes secrets...")
	if err := kubectl.UpsertSecret(d, "tfe-secrets", map[string]string{
		"TFE_LICENSE":             creds.License,
		"TFE_ENCRYPTION_PASSWORD": creds.EncryptionPassword,
	}); err != nil {
		return err
	}

	if err := kubectl.UpsertTLSSecret(d, "tfe-tls", creds.TLSCert, creds.TLSKey); err != nil {
		return fmt.Errorf("create TLS secret: %w", err)
	}

	if d.Mode == state.ModeExternal || d.Mode == state.ModeActiveActive {
		storageData := map[string]string{
			"TFE_DATABASE_URL":         creds.DatabaseURL,
			"TFE_S3_BUCKET":            creds.S3Bucket,
			"TFE_S3_REGION":            creds.S3Region,
			"TFE_S3_ACCESS_KEY_ID":     creds.S3AccessKeyID,
			"TFE_S3_SECRET_ACCESS_KEY": creds.S3SecretAccessKey,
		}
		if d.Mode == state.ModeActiveActive {
			storageData["TFE_REDIS_URL"] = creds.RedisURL
		}
		if err := kubectl.UpsertSecret(d, "tfe-storage", storageData); err != nil {
			return err
		}
	}

	// 5. Generate values.yaml
	fmt.Println("→ Generating Helm values...")
	valuesPath, err := helm.WriteValues(d)
	if err != nil {
		return fmt.Errorf("generate values: %w", err)
	}

	// 6. Helm repo
	fmt.Println("→ Adding HashiCorp Helm repo...")
	if err := retry.Do("helm repo add", retry.DefaultCloud, func(_ *bytes.Buffer) error {
		return helm.RepoAdd(d)
	}); err != nil {
		return err
	}
	if err := retry.Do("helm repo update", retry.DefaultCloud, func(_ *bytes.Buffer) error {
		return helm.RepoUpdate(d)
	}); err != nil {
		return err
	}

	// 7. Helm install (streams live output; retried on transient failures)
	fmt.Println("→ Installing Terraform Enterprise via Helm (this may take several minutes)...")
	timeout := "10m"
	if err := retry.Do("helm install", retry.DefaultK8s, func(buf *bytes.Buffer) error {
		return helm.Install(d, valuesPath, timeout, buf)
	}); err != nil {
		diagnostics.DiagnoseK8s(d.Namespace, "tfe")
		p.diagnoseCloud()
		return fmt.Errorf("helm install: %w", err)
	}

	// 8. Save state
	d.Status = state.StatusRunning
	d.UpdatedAt = time.Now()
	if err := state.Save(d); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	// 9. Health summary
	fmt.Println("\n→ Deployment complete. Pod status:")
	_ = kubectl.GetPods(d)
	fmt.Printf("\nTFE URL: https://%s\n", d.Hostname)
	fmt.Println("Note: TFE may take a few minutes to initialize after pods are Running.")
	return nil
}

func (p *K8sProvisioner) Destroy(force bool) error {
	d := p.deployment
	fmt.Println("→ Uninstalling Helm release...")
	if err := helm.Uninstall(d); err != nil && !force {
		return err
	}
	fmt.Printf("→ Deleting namespace %q...\n", d.Namespace)
	if err := kubectl.DeleteNamespace(d); err != nil && !force {
		return err
	}
	if err := state.Delete(d.Name); err != nil {
		return fmt.Errorf("remove state: %w", err)
	}
	fmt.Printf("Deployment %q destroyed.\n", d.Name)
	return nil
}

func (p *K8sProvisioner) Status() (*ProvisionerStatus, error) {
	d := p.deployment
	if err := kubectl.GetPods(d); err != nil {
		return &ProvisionerStatus{
			DeploymentStatus: state.StatusUnknown,
			Message:          err.Error(),
		}, nil
	}
	return &ProvisionerStatus{
		DeploymentStatus: d.Status,
		URL:              fmt.Sprintf("https://%s", d.Hostname),
	}, nil
}

func (p *K8sProvisioner) configureKubeconfig(creds *credentials.TFECredentials) error {
	d := p.deployment
	switch d.ClusterType {
	case state.ClusterEKS:
		return cloud.ConfigureEKSKubeconfig(d, creds.AWSProfile)
	case state.ClusterAKS:
		return cloud.ConfigureAKSKubeconfig(d, creds)
	case state.ClusterGKE:
		return cloud.ConfigureGKEKubeconfig(d, creds)
	case state.ClusterKubeadm:
		return nil
	default:
		return fmt.Errorf("unknown cluster type: %s", d.ClusterType)
	}
}

// diagnoseCloud queries the appropriate cloud provider's logs after a failure.
func (p *K8sProvisioner) diagnoseCloud() {
	d := p.deployment
	switch d.ClusterType {
	case state.ClusterEKS:
		diagnostics.CloudTrailEKSEvents(d.EKSRegion)
	case state.ClusterAKS:
		diagnostics.AzureActivityLogErrors(d.AKSResourceGroup)
	case state.ClusterGKE:
		diagnostics.GCPClusterErrors(d.GKEProject, d.GKEClusterName)
	}
}
