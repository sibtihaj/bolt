package state

import "time"

type Backend string

const (
	BackendK8s    Backend = "k8s"
	BackendDocker Backend = "docker"
)

type OperationalMode string

const (
	ModeDisk         OperationalMode = "disk"
	ModeExternal     OperationalMode = "external"
	ModeActiveActive OperationalMode = "active-active"
)

type ClusterType string

const (
	ClusterEKS     ClusterType = "eks"
	ClusterAKS     ClusterType = "aks"
	ClusterGKE     ClusterType = "gke"
	ClusterKubeadm ClusterType = "kubeadm"
)

type DeploymentStatus string

const (
	StatusPending DeploymentStatus = "pending"
	StatusRunning DeploymentStatus = "running"
	StatusFailed  DeploymentStatus = "failed"
	StatusUnknown DeploymentStatus = "unknown"
)

type TFEDeployment struct {
	Name             string          `json:"name"`
	Backend          Backend         `json:"backend"`
	Mode             OperationalMode `json:"mode"`
	ClusterType      ClusterType     `json:"cluster_type,omitempty"`
	Namespace        string          `json:"namespace,omitempty"`
	Hostname         string          `json:"hostname"`
	ImageTag         string          `json:"image_tag"`
	HelmChartVersion string          `json:"helm_chart_version,omitempty"`
	Kubeconfig       string          `json:"kubeconfig,omitempty"`
	TLSCertPath      string          `json:"tls_cert_path"`
	TLSKeyPath       string          `json:"tls_key_path"`
	SelfSignedTLS    bool            `json:"self_signed_tls"`
	DataDir          string          `json:"data_dir,omitempty"`
	SSHHost          string          `json:"ssh_host,omitempty"`
	SSHUser          string          `json:"ssh_user,omitempty"`
	SSHKeyPath       string          `json:"ssh_key_path,omitempty"`
	// Cloud cluster identifiers (not secrets)
	EKSClusterName  string `json:"eks_cluster_name,omitempty"`
	EKSRegion       string `json:"eks_region,omitempty"`
	GKEClusterName  string `json:"gke_cluster_name,omitempty"`
	GKEZone         string `json:"gke_zone,omitempty"`
	GKEProject      string `json:"gke_project,omitempty"`
	AKSClusterName  string `json:"aks_cluster_name,omitempty"`
	AKSResourceGroup string `json:"aks_resource_group,omitempty"`
	// Storage references (non-secret, just names/regions)
	StorageConfig *StorageConfig   `json:"storage_config,omitempty"`
	Status        DeploymentStatus `json:"status"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// StorageConfig holds non-secret storage references. Credentials (access
// keys, passwords, connection strings) are never written to disk.
type StorageConfig struct {
	S3Bucket string `json:"s3_bucket,omitempty"`
	S3Region string `json:"s3_region,omitempty"`
}
