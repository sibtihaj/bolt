package infra

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsinfra "github.com/sibtihaj/bolt/app/infra/aws"
	azureinfra "github.com/sibtihaj/bolt/app/infra/azure"
	"github.com/sibtihaj/bolt/app/infra/errs"
	gcpinfra "github.com/sibtihaj/bolt/app/infra/gcp"
	k8sinfra "github.com/sibtihaj/bolt/app/infra/k8s"
	"github.com/sibtihaj/bolt/app/preflight"
	"github.com/sibtihaj/bolt/app/state"
)

// CredentialExpiredError is returned when any AWS call fails with an auth or
// token-expiry error mid-provisioning.  The cmd layer uses it to trigger
// re-authentication and retry.
type CredentialExpiredError struct {
	Cause error
}

func (e *CredentialExpiredError) Error() string        { return "AWS credentials expired: " + e.Cause.Error() }
func (e *CredentialExpiredError) Unwrap() error        { return e.Cause }
func (e *CredentialExpiredError) Kind() errs.ErrorKind { return errs.KindBadCredential }
func (e *CredentialExpiredError) Resource() string     { return "AWS credentials" }

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

// promoteCredentialError wraps err in CredentialExpiredError if it is an AWS
// auth or token-expiry failure.  All other errors pass through unchanged.
// Call this on every error returned from provisioning steps.
func promoteCredentialError(err error) error {
	if err != nil && errs.IsCredentialError(err) {
		return &CredentialExpiredError{Cause: err}
	}
	return err
}

// ── AWS ───────────────────────────────────────────────────────────────────────

func provisionAWS(ctx context.Context, cfg *InfraConfig, infraState *state.InfraState, out *InfraOutputs) (*InfraOutputs, error) {
	defer globalSpinner.stop() // safety net: stop spinner on any return path

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
		var vpcOut *awsinfra.VPCOutputs
		if cfg.AWS.ExistingVPCID != "" {
			// Heal path: caller resolved a VpcLimitExceeded by picking an existing VPC.
			step("Adopting existing VPC " + cfg.AWS.ExistingVPCID + "…")
			vpcOut, err = awsinfra.AdoptVPC(ctx, awsCfg, cfg.AWS.ExistingVPCID, cfg.NamePrefix)
			if err != nil {
				return nil, promoteCredentialError(fmt.Errorf("VPC adoption: %w", err))
			}
			done("VPC adopted: " + vpcOut.VPCID)

			step("Validating VPC capacity for EKS…")
			if err = awsinfra.ValidateVPCForEKS(ctx, awsCfg, cfg.AWS.ExistingVPCID, vpcOut, cfg.Sizing.Nodes.NodeCount); err != nil {
				return nil, fmt.Errorf("VPC capacity check: %w", err)
			}
			done("VPC capacity validated")
		} else {
			step("Provisioning VPC…")
			vpcOut, err = awsinfra.EnsureVPC(ctx, awsCfg, cfg.NamePrefix, cfg.AWS.Region, cfg.Tags)
			if err != nil {
				return nil, promoteCredentialError(err)
			}
			done("VPC ready: " + vpcOut.VPCID)
		}
		infraState.VPCID = vpcOut.VPCID
		infraState.SubnetIDs = append(vpcOut.PublicSubnetIDs, vpcOut.PrivateSubnetIDs...)

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

		var kubeconfigPath string
		if cfg.AWS.ExistingEKSClusterName != "" {
			// Explicit adopt chosen by heal handler — just write the kubeconfig.
			step("Configuring access to existing EKS cluster " + cfg.AWS.ExistingEKSClusterName + "…")
			kubeconfigPath, err = awsinfra.WriteEKSKubeconfig(ctx, awsCfg, cfg.AWS.ExistingEKSClusterName, cfg.AWS.Region)
			if err != nil {
				return nil, promoteCredentialError(err)
			}
			done("EKS cluster configured: " + cfg.AWS.ExistingEKSClusterName)
			infraState.EKSClusterCreated = cfg.AWS.ExistingEKSClusterName
		} else {
			// Check whether a cluster with this name already exists.
			detail, detailErr := awsinfra.GetEKSClusterDetail(ctx, awsCfg, eksCfg.ClusterName)
			var clusterStepMsg string
			if detailErr == nil {
				if detail.Tags["bolt:deployment"] != eksCfg.ClusterName {
					// External cluster — surface for interactive handling.
					globalSpinner.stop()
					return nil, &awsinfra.EKSClusterExistsError{
						Config:      awsCfg,
						ClusterName: eksCfg.ClusterName,
						Status:      detail.Status,
						Version:     detail.Version,
					}
				}
				// Bolt-managed cluster from a prior run — adopt silently.
				clusterStepMsg = fmt.Sprintf("Adopting bolt-managed EKS cluster %q (%s)…", eksCfg.ClusterName, detail.Status)
			} else {
				clusterStepMsg = "Provisioning EKS cluster  (≈15 min)…"
			}

			step(clusterStepMsg)
			if err := liveWait(ctx, "EKS cluster "+eksCfg.ClusterName,
				func() string {
					sCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					return awsinfra.EKSClusterStatus(sCtx, awsCfg, eksCfg.ClusterName)
				},
				func() error {
					var innerErr error
					kubeconfigPath, innerErr = awsinfra.EnsureEKSCluster(ctx, awsCfg, eksCfg)
					return innerErr
				},
			); err != nil {
				return nil, promoteCredentialError(err)
			}
			infraState.EKSClusterCreated = cfg.NamePrefix
		}
		out.KubeconfigPath = kubeconfigPath
	}

	// Phase 2 — S3 bucket: auto-retry with letter suffixes on global name conflict.
	desiredBucket := cfg.NamePrefix + "-tfe"
	if infraState.S3BucketCreated != "" {
		desiredBucket = infraState.S3BucketCreated // idempotent retry: reuse prior name
	} else if cfg.AWS.S3BucketOverride != "" {
		desiredBucket = cfg.AWS.S3BucketOverride
	}
	step("Provisioning S3 bucket…")
	bucketName, s3Err := ensureS3BucketWithRetry(ctx, awsCfg, desiredBucket, cfg.AWS.Region)
	if s3Err != nil {
		return nil, promoteCredentialError(s3Err)
	}
	infraState.S3BucketCreated = bucketName
	out.S3Bucket = bucketName
	out.S3Region = cfg.AWS.Region
	// Retrieve resolved credentials for S3 access keys.
	resolvedCreds, credErr := awsCfg.Credentials.Retrieve(ctx)
	if credErr == nil {
		out.S3AccessKeyID = resolvedCreds.AccessKeyID
		out.S3SecretKey = resolvedCreds.SecretAccessKey
	}
	done("S3 bucket ready: " + bucketName)

	// Database.
	dbPass := generatePassword(24)
	switch cfg.Database {
	case DBManaged:
		// Phase 3 — RDS PostgreSQL.
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
		step("Provisioning RDS PostgreSQL instance  (≈10 min)…")
		var dbURL string
		if err := liveWait(ctx, "RDS instance "+rdsCfg.InstanceID,
			func() string {
				sCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return awsinfra.RDSInstanceStatus(sCtx, awsCfg, rdsCfg.InstanceID)
			},
			func() error {
				var innerErr error
				dbURL, innerErr = awsinfra.EnsureRDSPostgres(ctx, awsCfg, rdsCfg)
				return innerErr
			},
		); err != nil {
			return nil, promoteCredentialError(err)
		}
		infraState.RDSInstanceID = rdsCfg.InstanceID
		out.DatabaseURL = dbURL

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
	defer globalSpinner.stop()

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
	defer globalSpinner.stop()

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

func step(msg string) { globalSpinner.start(msg) }
func done(msg string) { globalSpinner.success(msg) }

// liveWait runs opFn in a background goroutine while printing a status update
// every 60 seconds until it completes.  statusFn (may be nil) is called each
// tick to fetch the actual AWS resource status shown to the user.  On success
// it prints the elapsed time via the spinner; on failure it stops the spinner
// and returns the error.
func liveWait(ctx context.Context, label string, statusFn func() string, opFn func() error) error {
	type result struct{ err error }
	resultCh := make(chan result, 1)
	go func() { resultCh <- result{opFn()} }()

	reassurances := []string{
		"I'm alive and working on your behalf ⚡",
		"Haven't frozen — AWS is building your infrastructure",
		"Still working, hang tight ☕",
		"Good things take time — almost there",
		"AWS is processing your request 🔧",
		"I'm here, just waiting on AWS ⏳",
		"Progress is being made, trust the process ✨",
		"Cloud resources take a moment to warm up ☁️",
	}
	msgIdx := 0
	start := time.Now()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case r := <-resultCh:
			elapsed := time.Since(start).Round(time.Second)
			if r.err == nil {
				globalSpinner.success(fmt.Sprintf("%s complete  (%s)", label, elapsed))
			} else {
				globalSpinner.stop()
			}
			return r.err
		case <-ticker.C:
			elapsed := time.Since(start).Round(time.Second)
			msg := reassurances[msgIdx%len(reassurances)]
			msgIdx++
			if statusFn != nil {
				status := statusFn()
				globalSpinner.info(fmt.Sprintf("  ⋯  %s — AWS status: %s  [%s elapsed]\n     %s", label, status, elapsed, msg))
			} else {
				globalSpinner.info(fmt.Sprintf("  ⋯  %s — still in progress  [%s elapsed]\n     %s", label, elapsed, msg))
			}
		}
	}
}

// ensureS3BucketWithRetry creates desired bucket, and if the global name is
// already taken by another account, retries with single-letter suffixes (-a …
// -z).  Returns the name of the bucket actually created.  If all 26 variants
// are also taken it returns the last S3NameConflictError for the cmd layer to
// handle interactively.
func ensureS3BucketWithRetry(ctx context.Context, cfg aws.Config, desired, region string) (string, error) {
	_, err := awsinfra.EnsureS3Bucket(ctx, cfg, desired, region)
	if err == nil {
		return desired, nil
	}
	var conflict *awsinfra.S3NameConflictError
	if !errors.As(err, &conflict) {
		return "", err
	}

	prev := desired
	for c := 'a'; c <= 'z'; c++ {
		name := desired + "-" + string(c)
		globalSpinner.info(fmt.Sprintf("  !  Bucket %q is globally taken — trying %q", prev, name))
		prev = name
		_, err = awsinfra.EnsureS3Bucket(ctx, cfg, name, region)
		if err == nil {
			return name, nil
		}
		if !errors.As(err, &conflict) {
			return "", err
		}
	}
	return "", err // all variants taken — propagate to interactive heal handler
}

// generatePassword creates a cryptographically random, URL-safe password of
// the given byte length.
func generatePassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
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
	return region
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
