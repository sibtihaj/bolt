package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/credentials"
	"github.com/sibtihaj/bolt/app/helm"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
)

var k8sOpts struct {
	Name             string
	ClusterType      string
	Namespace        string
	Mode             string
	Hostname         string
	Kubeconfig       string
	HelmChartVersion string
	ImageTag         string
	WaitTimeout      string
	DryRun           bool
	GenerateTLS      bool
	// Credential flags
	License             string
	LicensePath         string
	EncryptionPassword  string
	TLSCertPath         string
	TLSKeyPath          string
	DatabaseURL         string
	S3Bucket            string
	S3Region            string
	S3AccessKeyID       string
	S3SecretAccessKey   string
	RedisURL            string
	AWSProfile          string
	GCPSAKeyPath        string
	AzureClientID       string
	AzureClientSecret   string
	AzureTenantID       string
	AzureSubscriptionID string
	// Cloud cluster identifiers
	EKSClusterName   string
	EKSRegion        string
	GKEClusterName   string
	GKEZone          string
	GKEProject       string
	AKSClusterName   string
	AKSResourceGroup string
}

var deployK8sCmd = &cobra.Command{
	Use:   "k8s",
	Short: "Deploy TFE on Kubernetes using Helm",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		validModes := map[string]bool{"disk": true, "external": true, "active-active": true}
		if !validModes[k8sOpts.Mode] {
			return fmt.Errorf("--mode must be disk, external, or active-active (got %q)", k8sOpts.Mode)
		}
		validTypes := map[string]bool{"eks": true, "aks": true, "gke": true, "kubeadm": true}
		if !validTypes[k8sOpts.ClusterType] {
			return fmt.Errorf("--cluster-type must be eks, aks, gke, or kubeadm (got %q)", k8sOpts.ClusterType)
		}
		if k8sOpts.Mode != "disk" && k8sOpts.DatabaseURL == "" && os.Getenv("TFE_DATABASE_URL") == "" {
			return fmt.Errorf("--db-url (or TFE_DATABASE_URL) is required for mode %q", k8sOpts.Mode)
		}
		if k8sOpts.Mode == "active-active" && k8sOpts.RedisURL == "" && os.Getenv("TFE_REDIS_URL") == "" {
			return fmt.Errorf("--redis-url (or TFE_REDIS_URL) is required for active-active mode")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := credentials.Resolve(credentials.Flags{
			License: k8sOpts.License, LicensePath: k8sOpts.LicensePath,
			EncryptionPassword: k8sOpts.EncryptionPassword,
			TLSCertPath: k8sOpts.TLSCertPath, TLSKeyPath: k8sOpts.TLSKeyPath,
			DatabaseURL: k8sOpts.DatabaseURL, S3Bucket: k8sOpts.S3Bucket,
			S3Region: k8sOpts.S3Region, S3AccessKeyID: k8sOpts.S3AccessKeyID,
			S3SecretAccessKey: k8sOpts.S3SecretAccessKey, RedisURL: k8sOpts.RedisURL,
			AWSProfile: k8sOpts.AWSProfile, GCPSAKeyPath: k8sOpts.GCPSAKeyPath,
			AzureClientID: k8sOpts.AzureClientID, AzureClientSecret: k8sOpts.AzureClientSecret,
			AzureTenantID: k8sOpts.AzureTenantID, AzureSubscriptionID: k8sOpts.AzureSubscriptionID,
		}, globalConfig)
		if err != nil {
			return err
		}

		imageTag := k8sOpts.ImageTag
		if imageTag == "" {
			imageTag = "latest"
		}
		if globalConfig.DefaultImageTag != "" && imageTag == "latest" {
			imageTag = globalConfig.DefaultImageTag
		}

		kubeconfig := k8sOpts.Kubeconfig
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(home, ".kube", "config")
		}

		d := &state.TFEDeployment{
			Name:             k8sOpts.Name,
			Backend:          state.BackendK8s,
			Mode:             state.OperationalMode(k8sOpts.Mode),
			ClusterType:      state.ClusterType(k8sOpts.ClusterType),
			Namespace:        k8sOpts.Namespace,
			Hostname:         k8sOpts.Hostname,
			ImageTag:         imageTag,
			HelmChartVersion: k8sOpts.HelmChartVersion,
			Kubeconfig:       kubeconfig,
			TLSCertPath:      creds.TLSCert,
			TLSKeyPath:       creds.TLSKey,
			SelfSignedTLS:    k8sOpts.GenerateTLS,
			EKSClusterName:   k8sOpts.EKSClusterName,
			EKSRegion:        k8sOpts.EKSRegion,
			GKEClusterName:   k8sOpts.GKEClusterName,
			GKEZone:          k8sOpts.GKEZone,
			GKEProject:       k8sOpts.GKEProject,
			AKSClusterName:   k8sOpts.AKSClusterName,
			AKSResourceGroup: k8sOpts.AKSResourceGroup,
			Status:           state.StatusPending,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
		if k8sOpts.S3Bucket != "" || creds.S3Bucket != "" {
			d.StorageConfig = &state.StorageConfig{S3Bucket: creds.S3Bucket, S3Region: creds.S3Region}
		}

		// Dry-run: print rendered values.yaml and exit
		if k8sOpts.DryRun {
			path, err := helm.WriteValues(d)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Printf("# Dry-run: generated values.yaml (%s)\n\n", path)
			fmt.Println(string(data))
			return nil
		}

		// Handle self-signed TLS before provisioning (TLS paths needed in deployment)
		if k8sOpts.GenerateTLS {
			home, _ := os.UserHomeDir()
			tlsDir := filepath.Join(home, ".bolt", "tls", d.Name)
			certPath := filepath.Join(tlsDir, "tfe.crt")
			keyPath := filepath.Join(tlsDir, "tfe.key")
			if err := os.MkdirAll(tlsDir, 0700); err != nil {
				return err
			}
			d.TLSCertPath = certPath
			d.TLSKeyPath = keyPath
			creds.TLSCert = certPath
			creds.TLSKey = keyPath
		}

		p, err := tfe.NewProvisioner(d)
		if err != nil {
			return err
		}
		return p.Deploy(creds)
	},
}

func init() {
	deployCmd.AddCommand(deployK8sCmd)

	f := deployK8sCmd.Flags()
	f.StringVarP(&k8sOpts.Name, "name", "n", "", "deployment name (required)")
	f.StringVar(&k8sOpts.ClusterType, "cluster-type", "", "cluster type: eks, aks, gke, kubeadm (required)")
	f.StringVar(&k8sOpts.Namespace, "namespace", "tfe", "Kubernetes namespace")
	f.StringVar(&k8sOpts.Mode, "mode", "disk", "operational mode: disk, external, active-active")
	f.StringVar(&k8sOpts.Hostname, "hostname", "", "TFE FQDN (required)")
	f.StringVar(&k8sOpts.Kubeconfig, "kubeconfig", "", "path to kubeconfig (default: ~/.kube/config)")
	f.StringVar(&k8sOpts.HelmChartVersion, "helm-chart-version", "", "pin Helm chart version")
	f.StringVar(&k8sOpts.ImageTag, "image-tag", "latest", "TFE container image tag")
	f.StringVar(&k8sOpts.WaitTimeout, "wait-timeout", "10m", "helm --wait timeout")
	f.BoolVar(&k8sOpts.DryRun, "dry-run", false, "render values.yaml and exit without deploying")
	f.BoolVar(&k8sOpts.GenerateTLS, "generate-tls", false, "generate a self-signed TLS certificate (dev only)")
	// Credentials
	f.StringVar(&k8sOpts.License, "license", "", "TFE license string (or use TFE_LICENSE env)")
	f.StringVar(&k8sOpts.LicensePath, "license-path", "", "path to TFE license file (or use TFE_LICENSE_PATH env)")
	f.StringVar(&k8sOpts.EncryptionPassword, "encryption-password", "", "TFE encryption password")
	f.StringVar(&k8sOpts.TLSCertPath, "tls-cert", "", "path to TLS certificate PEM file")
	f.StringVar(&k8sOpts.TLSKeyPath, "tls-key", "", "path to TLS private key PEM file")
	f.StringVar(&k8sOpts.DatabaseURL, "db-url", "", "PostgreSQL connection URL (external/active-active)")
	f.StringVar(&k8sOpts.S3Bucket, "s3-bucket", "", "S3 bucket name")
	f.StringVar(&k8sOpts.S3Region, "s3-region", "", "S3 bucket region")
	f.StringVar(&k8sOpts.S3AccessKeyID, "s3-access-key", "", "S3 access key ID")
	f.StringVar(&k8sOpts.S3SecretAccessKey, "s3-secret-key", "", "S3 secret access key")
	f.StringVar(&k8sOpts.RedisURL, "redis-url", "", "Redis URL (active-active only)")
	f.StringVar(&k8sOpts.AWSProfile, "aws-profile", "", "AWS credentials profile (EKS)")
	f.StringVar(&k8sOpts.GCPSAKeyPath, "gcp-sa-key", "", "path to GCP service account key JSON (GKE)")
	f.StringVar(&k8sOpts.AzureClientID, "azure-client-id", "", "Azure service principal client ID (AKS)")
	f.StringVar(&k8sOpts.AzureClientSecret, "azure-client-secret", "", "Azure service principal client secret (AKS)")
	f.StringVar(&k8sOpts.AzureTenantID, "azure-tenant-id", "", "Azure tenant ID (AKS)")
	f.StringVar(&k8sOpts.AzureSubscriptionID, "azure-subscription-id", "", "Azure subscription ID (AKS)")
	// Cloud cluster identifiers
	f.StringVar(&k8sOpts.EKSClusterName, "eks-cluster-name", "", "EKS cluster name")
	f.StringVar(&k8sOpts.EKSRegion, "eks-region", "", "EKS cluster region")
	f.StringVar(&k8sOpts.GKEClusterName, "gke-cluster-name", "", "GKE cluster name")
	f.StringVar(&k8sOpts.GKEZone, "gke-zone", "", "GKE cluster zone")
	f.StringVar(&k8sOpts.GKEProject, "gke-project", "", "GCP project ID")
	f.StringVar(&k8sOpts.AKSClusterName, "aks-cluster-name", "", "AKS cluster name")
	f.StringVar(&k8sOpts.AKSResourceGroup, "aks-resource-group", "", "AKS resource group")

	_ = deployK8sCmd.MarkFlagRequired("name")
	_ = deployK8sCmd.MarkFlagRequired("cluster-type")
	_ = deployK8sCmd.MarkFlagRequired("hostname")
}
