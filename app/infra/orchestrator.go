package infra

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	awsinfra "github.com/sibtihaj/bolt/app/infra/aws"
	azureinfra "github.com/sibtihaj/bolt/app/infra/azure"
	gcpinfra "github.com/sibtihaj/bolt/app/infra/gcp"
	k8sinfra "github.com/sibtihaj/bolt/app/infra/k8s"
	"github.com/sibtihaj/bolt/app/preflight"
	"github.com/sibtihaj/bolt/app/state"
)

// Provision runs full infrastructure provisioning based on cfg. It updates
// infraState in-place with the ID of every resource it creates, so a partial
// run can be resumed or rolled back later.
//
// Returns InfraOutputs with all connection details needed to deploy TFE on top.
func Provision(ctx context.Context, cfg *InfraConfig, infraState *state.InfraState) (*InfraOutputs, error) {
	outputs := &InfraOutputs{ProvisionedAt: time.Now()}

	infraState.ProvisionMode = state.ProvisionMode(cfg.Mode)
	infraState.Cloud = string(cfg.Cloud)
	infraState.DatabaseChoice = state.DatabaseChoice(cfg.Database)

	switch cfg.Cloud {
	case CloudAWS:
		return provisionAWS(ctx, cfg, infraState, outputs)
	case CloudAzure:
		return provisionAzure(ctx, cfg, infraState, outputs)
	case CloudGCP:
		return provisionGCP(ctx, cfg, infraState, outputs)
	}
	return nil, fmt.Errorf("unsupported cloud provider: %q", cfg.Cloud)
}

// ── AWS ───────────────────────────────────────────────────────────────────────

func provisionAWS(ctx context.Context, cfg *InfraConfig, infraState *state.InfraState, out *InfraOutputs) (*InfraOutputs, error) {
	awsCfg, err := preflight.BuildAWSConfig(ctx, &preflight.AWSConfig{
		AssumeRoleARN:   cfg.AWS.AssumeRoleARN,
		Region:          cfg.AWS.Region,
		AccessKeyID:     cfg.AWS.AccessKeyID,
		SecretAccessKey: cfg.AWS.SecretAccessKey,
		SessionToken:    cfg.AWS.SessionToken,
	})
	if err != nil {
		return nil, fmt.Errorf("building AWS config: %w", err)
	}

	// Phase 4 — full cluster provisioning (VPC + EKS).
	if cfg.Mode == ProvisionAll {
		step("Provisioning VPC…")
		vpcOut, err := awsinfra.EnsureVPC(ctx, awsCfg, cfg.NamePrefix, cfg.AWS.Region, cfg.Tags)
		if err != nil {
			return nil, fmt.Errorf("VPC provisioning: %w", err)
		}
		infraState.VPCID = vpcOut.VPCID
		infraState.SubnetIDs = append(vpcOut.PublicSubnetIDs, vpcOut.PrivateSubnetIDs...)
		done("VPC ready: " + vpcOut.VPCID)

		step("Provisioning EKS cluster  (≈15 min)…")
		eksCfg := &awsinfra.EKSConfig{
			ClusterName:      cfg.NamePrefix,
			Region:           cfg.AWS.Region,
			SubnetIDs:        infraState.SubnetIDs,
			SecurityGroupIDs: []string{vpcOut.SecurityGroupID},
			NodeGroupName:    cfg.NamePrefix + "-nodes",
			NodeInstanceType: cfg.Sizing.Nodes.InstanceType,
			NodeDesiredCount: int32(cfg.Sizing.Nodes.NodeCount),
			NodeMinCount:     1,
			NodeMaxCount:     int32(cfg.Sizing.Nodes.NodeCount + 2),
			Tags:             cfg.Tags,
		}
		kubeconfigPath, err := awsinfra.EnsureEKSCluster(ctx, awsCfg, eksCfg)
		if err != nil {
			return nil, fmt.Errorf("EKS provisioning: %w", err)
		}
		infraState.EKSClusterCreated = cfg.NamePrefix
		out.KubeconfigPath = kubeconfigPath
		done("EKS cluster ready: " + cfg.NamePrefix)
	}

	// Phase 2 — S3 bucket.
	step("Provisioning S3 bucket…")
	bucketName := cfg.NamePrefix + "-tfe"
	if _, err := awsinfra.EnsureS3Bucket(ctx, awsCfg, bucketName, cfg.AWS.Region); err != nil {
		return nil, fmt.Errorf("S3 provisioning: %w", err)
	}
	infraState.S3BucketCreated = bucketName
	out.S3Bucket = bucketName
	out.S3Region = cfg.AWS.Region
	// Retrieve resolved credentials for S3 access keys.
	resolvedCreds, err := awsCfg.Credentials.Retrieve(ctx)
	if err == nil {
		out.S3AccessKeyID = resolvedCreds.AccessKeyID
		out.S3SecretKey = resolvedCreds.SecretAccessKey
	}
	done("S3 bucket ready: " + bucketName)

	// Database.
	dbPass := generatePassword(24)
	switch cfg.Database {
	case DBManaged:
		// Phase 3 — RDS PostgreSQL.
		step("Provisioning RDS PostgreSQL instance  (≈10 min)…")
		rdsCfg := &awsinfra.RDSConfig{
			InstanceID:    cfg.NamePrefix + "-db",
			InstanceClass: cfg.Sizing.DBClass,
			StorageGB:     int32(cfg.Sizing.DBStorage),
			DBName:        "tfe",
			MasterUser:    "tfe",
			MasterPass:    dbPass,
			Region:        cfg.AWS.Region,
			Tags:          cfg.Tags,
		}
		if len(infraState.SubnetIDs) > 0 {
			rdsCfg.VPCSecurityGroupIDs = []string{} // filled if VPC was provisioned
		}
		dbURL, err := awsinfra.EnsureRDSPostgres(ctx, awsCfg, rdsCfg)
		if err != nil {
			return nil, fmt.Errorf("RDS provisioning: %w", err)
		}
		infraState.RDSInstanceID = rdsCfg.InstanceID
		out.DatabaseURL = dbURL
		done("RDS instance ready: " + rdsCfg.InstanceID)

	case DBInCluster:
		// Phase 2 — in-cluster PostgreSQL StatefulSet.
		step("Deploying in-cluster PostgreSQL StatefulSet…")
		dbURL, err := k8sinfra.EnsureInClusterPostgres(&k8sinfra.InClusterPostgresConfig{
			Namespace:  "tfe",
			Password:   dbPass,
			StorageGB:  max(cfg.Sizing.DBStorage, 20),
			Kubeconfig: out.KubeconfigPath,
		})
		if err != nil {
			return nil, fmt.Errorf("in-cluster PostgreSQL: %w", err)
		}
		out.DatabaseURL = dbURL
		done("In-cluster PostgreSQL ready")
	}

	return out, nil
}

// ── Azure ─────────────────────────────────────────────────────────────────────

func provisionAzure(ctx context.Context, cfg *InfraConfig, infraState *state.InfraState, out *InfraOutputs) (*InfraOutputs, error) {
	step("Obtaining Azure access token…")
	token, err := preflight.GetAzureToken(ctx, &preflight.AzureConfig{
		TenantID:       cfg.Azure.TenantID,
		ClientID:       cfg.Azure.ClientID,
		ClientSecret:   cfg.Azure.ClientSecret,
		SubscriptionID: cfg.Azure.SubscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("Azure authentication: %w", err)
	}
	done("Azure token obtained")

	// Phase 2 — Azure Blob Storage.
	step("Provisioning Azure Blob Storage…")
	storageAccount := sanitizeStorageAccount(cfg.NamePrefix) + "tfe"
	containerName := "tfe-data"
	blobCfg := &azureinfra.BlobConfig{
		SubscriptionID: cfg.Azure.SubscriptionID,
		ResourceGroup:  cfg.Azure.ResourceGroup,
		StorageAccount: storageAccount,
		ContainerName:  containerName,
		Location:       cfg.Azure.Location,
		Token:          token,
	}
	endpoint, accessKey, err := azureinfra.EnsureBlob(ctx, blobCfg)
	if err != nil {
		return nil, fmt.Errorf("Azure Blob Storage: %w", err)
	}
	if cfg.Azure.ResourceGroup != "" {
		infraState.AzureRGCreated = cfg.Azure.ResourceGroup
	}
	infraState.AzureStorageAccount = storageAccount
	out.S3Bucket = containerName
	out.S3Endpoint = endpoint
	// Azure Blob S3-compat: account name as access key, storage key as secret
	out.S3AccessKeyID = storageAccount
	out.S3SecretKey = accessKey
	out.S3Region = cfg.Azure.Location
	done("Azure Blob Storage ready: " + storageAccount)

	// Database.
	dbPass := generatePassword(24)
	switch cfg.Database {
	case DBManaged:
		// Phase 3 — Azure DB for PostgreSQL Flexible Server.
		step("Provisioning Azure PostgreSQL Flexible Server  (≈10 min)…")
		pgCfg := &azureinfra.PostgresConfig{
			SubscriptionID: cfg.Azure.SubscriptionID,
			ResourceGroup:  cfg.Azure.ResourceGroup,
			ServerName:     cfg.NamePrefix + "-db",
			Location:       cfg.Azure.Location,
			AdminUser:      "tfe",
			AdminPass:      dbPass,
			SKUName:        cfg.Sizing.DBClass,
			SKUTier:        "GeneralPurpose",
			StorageGB:      cfg.Sizing.DBStorage,
			Token:          token,
		}
		dbURL, err := azureinfra.EnsurePostgres(ctx, pgCfg)
		if err != nil {
			return nil, fmt.Errorf("Azure PostgreSQL: %w", err)
		}
		infraState.AzurePostgresServer = pgCfg.ServerName
		out.DatabaseURL = dbURL
		done("Azure PostgreSQL ready: " + pgCfg.ServerName)

	case DBInCluster:
		step("Deploying in-cluster PostgreSQL StatefulSet…")
		dbURL, err := k8sinfra.EnsureInClusterPostgres(&k8sinfra.InClusterPostgresConfig{
			Namespace:  "tfe",
			Password:   dbPass,
			StorageGB:  max(cfg.Sizing.DBStorage, 20),
			Kubeconfig: out.KubeconfigPath,
		})
		if err != nil {
			return nil, fmt.Errorf("in-cluster PostgreSQL: %w", err)
		}
		out.DatabaseURL = dbURL
		done("In-cluster PostgreSQL ready")
	}

	return out, nil
}

// ── GCP ───────────────────────────────────────────────────────────────────────

func provisionGCP(ctx context.Context, cfg *InfraConfig, infraState *state.InfraState, out *InfraOutputs) (*InfraOutputs, error) {
	step("Obtaining GCP access token…")
	token, err := preflight.GetGCPToken(ctx, cfg.GCP.ServiceAcctJSON)
	if err != nil {
		return nil, fmt.Errorf("GCP authentication: %w", err)
	}
	done("GCP token obtained")

	// Phase 2 — GCS bucket.
	step("Provisioning GCS bucket…")
	bucketName := cfg.NamePrefix + "-tfe"
	gcsCfg := &gcpinfra.GCSConfig{
		ProjectID:  cfg.GCP.ProjectID,
		BucketName: bucketName,
		Location:   gcpRegionToLocation(cfg.GCP.Region),
		Token:      token,
	}
	if _, err := gcpinfra.EnsureGCSBucket(ctx, gcsCfg); err != nil {
		return nil, fmt.Errorf("GCS bucket: %w", err)
	}
	infraState.GCSBucketCreated = bucketName
	// TFE uses S3-compat params; for GCP we use HMAC keys (wired in Phase 3+).
	// For now pass the GCS JSON endpoint; TFE's GCS storage type will be used.
	out.S3Bucket = bucketName
	out.S3Region = cfg.GCP.Region
	out.S3Endpoint = "https://storage.googleapis.com"
	done("GCS bucket ready: " + bucketName)

	// Database.
	dbPass := generatePassword(24)
	switch cfg.Database {
	case DBManaged:
		// Phase 3 — Cloud SQL PostgreSQL.
		step("Provisioning Cloud SQL instance  (≈10 min)…")
		sqlCfg := &gcpinfra.CloudSQLConfig{
			ProjectID:    cfg.GCP.ProjectID,
			InstanceName: cfg.NamePrefix + "-db",
			Region:       cfg.GCP.Region,
			Tier:         cfg.Sizing.DBClass,
			StorageGB:    cfg.Sizing.DBStorage,
			DBName:       "tfe",
			UserName:     "tfe",
			UserPass:     dbPass,
			Token:        token,
		}
		dbURL, err := gcpinfra.EnsureCloudSQL(ctx, sqlCfg)
		if err != nil {
			return nil, fmt.Errorf("Cloud SQL: %w", err)
		}
		infraState.CloudSQLInstanceID = sqlCfg.InstanceName
		out.DatabaseURL = dbURL
		done("Cloud SQL ready: " + sqlCfg.InstanceName)

	case DBInCluster:
		step("Deploying in-cluster PostgreSQL StatefulSet…")
		dbURL, err := k8sinfra.EnsureInClusterPostgres(&k8sinfra.InClusterPostgresConfig{
			Namespace:  "tfe",
			Password:   dbPass,
			StorageGB:  max(cfg.Sizing.DBStorage, 20),
			Kubeconfig: out.KubeconfigPath,
		})
		if err != nil {
			return nil, fmt.Errorf("in-cluster PostgreSQL: %w", err)
		}
		out.DatabaseURL = dbURL
		done("In-cluster PostgreSQL ready")
	}

	return out, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func step(msg string) {
	fmt.Fprintf(os.Stdout, "  ⋯  %s\n", msg)
}

func done(msg string) {
	fmt.Fprintf(os.Stdout, "  ✓  %s\n", msg)
}

// generatePassword creates a cryptographically random, URL-safe password of
// the given byte length.
func generatePassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	// base64 URL-safe, no padding — trim to requested length
	s := base64.RawURLEncoding.EncodeToString(b)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func sanitizeStorageAccount(prefix string) string {
	result := make([]byte, 0, len(prefix))
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		}
	}
	s := string(result)
	if len(s) > 16 {
		s = s[:16]
	}
	return s
}

func gcpRegionToLocation(region string) string {
	// GCS uses multi-region location strings like "US", "EU", "ASIA"
	// for single regions use the region directly.
	return region
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
