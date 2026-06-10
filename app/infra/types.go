package infra

import "time"

// ProvisionMode controls what bolt creates vs what the caller brings.
type ProvisionMode string

const (
	// ProvisionAll — bolt creates VPC, cluster, database, and object storage from scratch.
	ProvisionAll ProvisionMode = "all"
	// ProvisionStorageOnly — caller provides an existing cluster; bolt creates DB + object storage.
	ProvisionStorageOnly ProvisionMode = "storage-only"
	// ProvisionBYO — caller provides everything; bolt only deploys TFE.
	ProvisionBYO ProvisionMode = "byo"
)

// CloudProvider identifies which public cloud to target.
type CloudProvider string

const (
	CloudAWS    CloudProvider = "aws"
	CloudAzure  CloudProvider = "azure"
	CloudGCP    CloudProvider = "gcp"
	CloudDocker CloudProvider = "docker"
)

// ResourceTier sets the pre-defined sizing profile for all provisioned resources.
type ResourceTier string

const (
	TierMinimum     ResourceTier = "minimum"     // smallest viable production footprint
	TierRecommended ResourceTier = "recommended" // HashiCorp reference sizing
	TierCustom      ResourceTier = "custom"      // user-specified instance types
)

// DatabaseChoice selects where PostgreSQL runs.
type DatabaseChoice string

const (
	DBManaged   DatabaseChoice = "managed"    // RDS / Cloud SQL / Azure DB for PostgreSQL
	DBInCluster DatabaseChoice = "in-cluster" // PostgreSQL StatefulSet inside K8s
	DBBYO       DatabaseChoice = "byo"        // caller supplies connection string
)

// AWSCreds holds credential material for AWS.  Never persisted to disk.
type AWSCreds struct {
	// If set, bolt calls STS AssumeRole using ambient or static creds below.
	AssumeRoleARN string
	Region        string
	// Static creds — used only when AssumeRoleARN is empty and no ambient creds exist.
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// AzureCreds holds credential material for Azure.  Never persisted to disk.
type AzureCreds struct {
	SubscriptionID string
	TenantID       string
	ClientID       string
	ClientSecret   string
	ResourceGroup  string // existing or to be created by bolt
	Location       string // e.g. "eastus"
}

// GCPCreds holds credential material for GCP.  Never persisted to disk.
type GCPCreds struct {
	ProjectID       string
	Region          string
	Zone            string
	ServiceAcctJSON string // path to service-account key file
}

// NodeSizing describes a single node group's compute resources.
type NodeSizing struct {
	InstanceType string // e.g. "m5.xlarge", "Standard_D4s_v3", "n2-standard-4"
	NodeCount    int
}

// ResourceSizing collects all sizing decisions for the provisioning run.
type ResourceSizing struct {
	Tier      ResourceTier
	Nodes     NodeSizing
	DBClass   string // e.g. "db.t3.large" for RDS, "db-custom-4-16384" for Cloud SQL
	DBStorage int    // GiB of persistent storage for the database
}

// DefaultSizing returns the pre-configured sizing for a given tier and cloud.
func DefaultSizing(tier ResourceTier, cloud CloudProvider) ResourceSizing {
	switch tier {
	case TierMinimum:
		switch cloud {
		case CloudAWS:
			return ResourceSizing{Tier: tier, Nodes: NodeSizing{"m5.large", 2}, DBClass: "db.t3.medium", DBStorage: 100}
		case CloudAzure:
			return ResourceSizing{Tier: tier, Nodes: NodeSizing{"Standard_D2s_v3", 2}, DBClass: "GP_Gen5_2", DBStorage: 100}
		case CloudGCP:
			return ResourceSizing{Tier: tier, Nodes: NodeSizing{"n2-standard-2", 2}, DBClass: "db-custom-2-7680", DBStorage: 100}
		}
	case TierRecommended:
		switch cloud {
		case CloudAWS:
			return ResourceSizing{Tier: tier, Nodes: NodeSizing{"m5.xlarge", 3}, DBClass: "db.r5.large", DBStorage: 200}
		case CloudAzure:
			return ResourceSizing{Tier: tier, Nodes: NodeSizing{"Standard_D4s_v3", 3}, DBClass: "GP_Gen5_4", DBStorage: 200}
		case CloudGCP:
			return ResourceSizing{Tier: tier, Nodes: NodeSizing{"n2-standard-4", 3}, DBClass: "db-custom-4-16384", DBStorage: 200}
		}
	}
	// TierCustom or unknown — return recommended AWS as safe default
	return ResourceSizing{Tier: TierCustom, Nodes: NodeSizing{"m5.xlarge", 3}, DBClass: "db.r5.large", DBStorage: 200}
}

// InfraConfig is the complete specification passed to any infra provisioner.
type InfraConfig struct {
	Mode     ProvisionMode
	Cloud    CloudProvider
	Database DatabaseChoice
	Sizing   ResourceSizing

	AWS   *AWSCreds
	Azure *AzureCreds
	GCP   *GCPCreds

	// NamePrefix is prepended to every cloud resource bolt creates.
	NamePrefix string
	// Tags are applied to all provisioned cloud resources.
	Tags map[string]string
	// StateDir is where bolt stores Terraform/Tofu state files.
	StateDir string
}

// InfraOutputs are the concrete connection details produced after provisioning.
// The TFE deployer consumes these regardless of which cloud was used.
type InfraOutputs struct {
	// KubeconfigPath is the path to the kubeconfig for the provisioned cluster.
	KubeconfigPath string

	// DatabaseURL is a postgres:// connection string.
	DatabaseURL string

	// Object storage expressed as S3-compatible params.
	// S3Endpoint is empty for native AWS; set for GCS/Azure compatibility endpoints.
	S3Bucket      string
	S3Region      string
	S3Endpoint    string
	S3AccessKeyID string
	S3SecretKey   string

	// LoadBalancerHostname is populated after the cluster is ready.
	LoadBalancerHostname string

	ProvisionedAt time.Time
}

// Provisioner is the interface each cloud backend implements.
type Provisioner interface {
	// ValidateCredentials confirms credentials are valid before any provisioning starts.
	ValidateCredentials(cfg *InfraConfig) error
	// EnsureObjectStorage creates (or validates an existing) bucket.
	EnsureObjectStorage(cfg *InfraConfig) (*InfraOutputs, error)
	// EnsureDatabase provisions a managed DB or validates in-cluster readiness.
	EnsureDatabase(cfg *InfraConfig) (string, error) // returns postgres:// URL
	// EnsureCluster provisions VPC + cluster (Phase 4).
	EnsureCluster(cfg *InfraConfig) (string, error) // returns kubeconfig path
	// Destroy tears down all resources bolt provisioned.
	Destroy(cfg *InfraConfig) error
}
