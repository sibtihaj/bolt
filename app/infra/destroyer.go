package infra

import (
	"context"
	"fmt"
	"time"

	awsinfra "github.com/sibtihaj/bolt/app/infra/aws"
	azureinfra "github.com/sibtihaj/bolt/app/infra/azure"
	gcpinfra "github.com/sibtihaj/bolt/app/infra/gcp"
	k8sinfra "github.com/sibtihaj/bolt/app/infra/k8s"
	"github.com/sibtihaj/bolt/app/preflight"
	"github.com/sibtihaj/bolt/app/state"
)

// DestroyConfig carries credentials needed to authenticate to the cloud
// provider during teardown. This mirrors InfraConfig but only needs the
// credential fields — no sizing or naming.
type DestroyConfig struct {
	Cloud string // matches InfraState.Cloud ("aws"|"azure"|"gcp")

	// AWS credentials
	AWS *AWSCreds

	// Azure credentials
	Azure *AzureCreds

	// GCP credentials — only ServiceAcctJSON needed for token
	GCP *GCPCreds

	// Namespace + kubeconfig for in-cluster Postgres deletion (optional)
	K8sNamespace  string
	KubeconfigPath string
}

// Destroy removes all cloud resources recorded in infraState.
// Resources are deleted in reverse-dependency order: application layer first
// (in-cluster Postgres, RDS/CloudSQL), then storage, then cluster, then VPC.
// Each step is best-effort — all errors are collected and returned together
// so that as much as possible is cleaned up even on partial failure.
func Destroy(ctx context.Context, cfg *DestroyConfig, infraState *state.InfraState) error {
	switch infraState.Cloud {
	case "aws":
		return destroyAWS(ctx, cfg, infraState)
	case "azure":
		return destroyAzure(ctx, cfg, infraState)
	case "gcp":
		return destroyGCP(ctx, cfg, infraState)
	}
	return fmt.Errorf("unknown cloud provider in state: %q", infraState.Cloud)
}

// ── AWS ───────────────────────────────────────────────────────────────────────

func destroyAWS(ctx context.Context, cfg *DestroyConfig, st *state.InfraState) error {
	if cfg.AWS == nil {
		return fmt.Errorf("AWS credentials required to destroy AWS resources")
	}
	awsCfg, err := preflight.BuildAWSConfig(ctx, &preflight.AWSConfig{
		AssumeRoleARN:   cfg.AWS.AssumeRoleARN,
		Region:          cfg.AWS.Region,
		AccessKeyID:     cfg.AWS.AccessKeyID,
		SecretAccessKey: cfg.AWS.SecretAccessKey,
		SessionToken:    cfg.AWS.SessionToken,
	})
	if err != nil {
		return fmt.Errorf("building AWS config for destroy: %w", err)
	}

	var errs []error

	// 1. In-cluster Postgres (lives inside the cluster — delete before EKS).
	if st.DatabaseChoice == state.DBInCluster && cfg.KubeconfigPath != "" {
		step("Removing in-cluster PostgreSQL…")
		if err := k8sinfra.DeleteInClusterPostgres(cfg.K8sNamespace, cfg.KubeconfigPath); err != nil {
			errs = append(errs, fmt.Errorf("in-cluster postgres: %w", err))
		} else {
			done("In-cluster PostgreSQL removed")
		}
	}

	// 2. RDS instance.
	if st.RDSInstanceID != "" {
		step(fmt.Sprintf("Deleting RDS instance %s…", st.RDSInstanceID))
		if err := awsinfra.DeleteRDSPostgres(ctx, awsCfg, st.RDSInstanceID); err != nil {
			errs = append(errs, fmt.Errorf("RDS %s: %w", st.RDSInstanceID, err))
		} else {
			done("RDS instance deleted")
		}
	}

	// 3. S3 bucket (drain objects then delete bucket).
	if st.S3BucketCreated != "" {
		step(fmt.Sprintf("Draining and deleting S3 bucket %s…", st.S3BucketCreated))
		if err := awsinfra.DeleteS3Bucket(ctx, awsCfg, st.S3BucketCreated); err != nil {
			errs = append(errs, fmt.Errorf("S3 %s: %w", st.S3BucketCreated, err))
		} else {
			done("S3 bucket deleted")
		}
	}

	// 4. EKS cluster (node groups first, then control plane).
	if st.EKSClusterCreated != "" {
		step(fmt.Sprintf("Deleting EKS cluster %s (this may take ~15 min)…", st.EKSClusterCreated))
		if err := awsinfra.DeleteEKSCluster(ctx, awsCfg, st.EKSClusterCreated); err != nil {
			errs = append(errs, fmt.Errorf("EKS cluster %s: %w", st.EKSClusterCreated, err))
		} else {
			done("EKS cluster deleted")
		}
	}

	// 5. VPC (subnets, route tables, SGs, IGW, then VPC).
	if st.VPCID != "" {
		step(fmt.Sprintf("Tearing down VPC %s…", st.VPCID))
		if err := awsinfra.DeleteVPC(ctx, awsCfg, st.VPCID); err != nil {
			errs = append(errs, fmt.Errorf("VPC %s: %w", st.VPCID, err))
		} else {
			done("VPC removed")
		}
	}

	return joinErrors(errs)
}

// ── Azure ─────────────────────────────────────────────────────────────────────

func destroyAzure(ctx context.Context, cfg *DestroyConfig, st *state.InfraState) error {
	if cfg.Azure == nil {
		return fmt.Errorf("Azure credentials required to destroy Azure resources")
	}

	token, err := azureOAuthToken(ctx, cfg.Azure)
	if err != nil {
		return fmt.Errorf("Azure auth for destroy: %w", err)
	}

	var errs []error
	sub := cfg.Azure.SubscriptionID
	rg := st.AzureRGCreated
	if rg == "" {
		rg = cfg.Azure.ResourceGroup
	}

	// 1. In-cluster Postgres.
	if st.DatabaseChoice == state.DBInCluster && cfg.KubeconfigPath != "" {
		step("Removing in-cluster PostgreSQL…")
		if err := k8sinfra.DeleteInClusterPostgres(cfg.K8sNamespace, cfg.KubeconfigPath); err != nil {
			errs = append(errs, fmt.Errorf("in-cluster postgres: %w", err))
		} else {
			done("In-cluster PostgreSQL removed")
		}
	}

	// 2. PostgreSQL Flexible Server.
	if st.AzurePostgresServer != "" {
		step(fmt.Sprintf("Deleting Azure PostgreSQL server %s…", st.AzurePostgresServer))
		if err := azureinfra.DeletePostgresServer(ctx, sub, rg, st.AzurePostgresServer, token); err != nil {
			errs = append(errs, fmt.Errorf("Azure postgres %s: %w", st.AzurePostgresServer, err))
		} else {
			done("Azure PostgreSQL server deleted")
		}
	}

	// 3. Blob storage account.
	if st.AzureStorageAccount != "" {
		step(fmt.Sprintf("Deleting Azure storage account %s…", st.AzureStorageAccount))
		if err := azureinfra.DeleteStorageAccount(ctx, sub, rg, st.AzureStorageAccount, token); err != nil {
			errs = append(errs, fmt.Errorf("Azure storage %s: %w", st.AzureStorageAccount, err))
		} else {
			done("Azure storage account deleted")
		}
	}

	// 4. AKS cluster.
	if st.AKSClusterCreated != "" {
		step(fmt.Sprintf("Deleting AKS cluster %s (this may take ~10 min)…", st.AKSClusterCreated))
		if err := azureinfra.DeleteAKSCluster(ctx, sub, rg, st.AKSClusterCreated, token); err != nil {
			errs = append(errs, fmt.Errorf("AKS cluster %s: %w", st.AKSClusterCreated, err))
		} else {
			done("AKS cluster deleted")
		}
	}

	return joinErrors(errs)
}

// ── GCP ───────────────────────────────────────────────────────────────────────

func destroyGCP(ctx context.Context, cfg *DestroyConfig, st *state.InfraState) error {
	if cfg.GCP == nil {
		return fmt.Errorf("GCP credentials required to destroy GCP resources")
	}

	token, err := preflight.GetGCPToken(ctx, cfg.GCP.ServiceAcctJSON)
	if err != nil {
		return fmt.Errorf("GCP auth for destroy: %w", err)
	}

	var errs []error
	projectID := cfg.GCP.ProjectID

	// 1. In-cluster Postgres.
	if st.DatabaseChoice == state.DBInCluster && cfg.KubeconfigPath != "" {
		step("Removing in-cluster PostgreSQL…")
		if err := k8sinfra.DeleteInClusterPostgres(cfg.K8sNamespace, cfg.KubeconfigPath); err != nil {
			errs = append(errs, fmt.Errorf("in-cluster postgres: %w", err))
		} else {
			done("In-cluster PostgreSQL removed")
		}
	}

	// 2. Cloud SQL instance.
	if st.CloudSQLInstanceID != "" {
		step(fmt.Sprintf("Deleting Cloud SQL instance %s…", st.CloudSQLInstanceID))
		if err := gcpinfra.DeleteCloudSQLInstance(ctx, projectID, st.CloudSQLInstanceID, token); err != nil {
			errs = append(errs, fmt.Errorf("Cloud SQL %s: %w", st.CloudSQLInstanceID, err))
		} else {
			done("Cloud SQL instance deleted")
		}
	}

	// 3. GCS bucket.
	if st.GCSBucketCreated != "" {
		step(fmt.Sprintf("Draining and deleting GCS bucket %s…", st.GCSBucketCreated))
		gcsCfg := &gcpinfra.GCSConfig{
			ProjectID:  projectID,
			BucketName: st.GCSBucketCreated,
			Token:      token,
		}
		if err := gcpinfra.DeleteGCSBucket(ctx, gcsCfg); err != nil {
			errs = append(errs, fmt.Errorf("GCS bucket %s: %w", st.GCSBucketCreated, err))
		} else {
			done("GCS bucket deleted")
		}
	}

	// 4. GKE cluster.
	if st.GKEClusterCreated != "" {
		step(fmt.Sprintf("Deleting GKE cluster %s (this may take ~10 min)…", st.GKEClusterCreated))
		if err := gcpinfra.DeleteGKECluster(ctx, projectID, cfg.GCP.Zone, st.GKEClusterCreated, token); err != nil {
			errs = append(errs, fmt.Errorf("GKE cluster %s: %w", st.GKEClusterCreated, err))
		} else {
			done("GKE cluster deleted")
		}
	}

	return joinErrors(errs)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func azureOAuthToken(ctx context.Context, creds *AzureCreds) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cfg := &preflight.AzureConfig{
		SubscriptionID: creds.SubscriptionID,
		TenantID:       creds.TenantID,
		ClientID:       creds.ClientID,
		ClientSecret:   creds.ClientSecret,
		ResourceGroup:  creds.ResourceGroup,
		Location:       creds.Location,
	}
	return preflight.GetAzureToken(ctx, cfg)
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msg := fmt.Sprintf("%d error(s) during teardown:", len(errs))
	for i, e := range errs {
		msg += fmt.Sprintf("\n  %d. %s", i+1, e)
	}
	return fmt.Errorf("%s", msg)
}
