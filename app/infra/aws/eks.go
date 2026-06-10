// Phase 4: EKS cluster provisioning.
package aws

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// EKSConfig holds parameters for provisioning an EKS cluster.
type EKSConfig struct {
	ClusterName     string
	KubernetesVersion string // e.g. "1.30"
	Region          string
	SubnetIDs       []string
	SecurityGroupIDs []string
	NodeGroupName   string
	NodeInstanceType string
	NodeDesiredCount int32
	NodeMinCount     int32
	NodeMaxCount     int32
	Tags            map[string]string
}

// EnsureEKSCluster creates (or reuses) an EKS cluster and managed node group,
// then writes the kubeconfig and returns its path.
//
// Phase 4 implementation.
func EnsureEKSCluster(ctx context.Context, cfg aws.Config, ecfg *EKSConfig) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()

	client := eks.NewFromConfig(cfg)

	// Create IAM roles for cluster + node group if they do not exist.
	clusterRoleARN, err := ensureEKSClusterRole(ctx, cfg, ecfg.ClusterName+"-cluster-role")
	if err != nil {
		return "", fmt.Errorf("cluster IAM role: %w", err)
	}
	nodeRoleARN, err := ensureEKSNodeRole(ctx, cfg, ecfg.ClusterName+"-node-role")
	if err != nil {
		return "", fmt.Errorf("node IAM role: %w", err)
	}

	// Check whether the cluster already exists.
	_, err = client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(ecfg.ClusterName),
	})
	if err != nil {
		// Create cluster.
		k8sVersion := ecfg.KubernetesVersion
		if k8sVersion == "" {
			k8sVersion = "1.30"
		}
		createIn := &eks.CreateClusterInput{
			Name:    aws.String(ecfg.ClusterName),
			Version: aws.String(k8sVersion),
			RoleArn: aws.String(clusterRoleARN),
			ResourcesVpcConfig: &types.VpcConfigRequest{
				SubnetIds:        ecfg.SubnetIDs,
				SecurityGroupIds: ecfg.SecurityGroupIDs,
			},
			Tags: ecfg.Tags,
		}
		if _, err := client.CreateCluster(ctx, createIn); err != nil {
			return "", fmt.Errorf("creating EKS cluster %q: %w", ecfg.ClusterName, err)
		}
	}

	// Wait for cluster to become ACTIVE.
	if err := waitEKSClusterActive(ctx, client, ecfg.ClusterName); err != nil {
		return "", err
	}

	// Ensure node group.
	if err := ensureNodeGroup(ctx, client, ecfg, nodeRoleARN); err != nil {
		return "", err
	}

	// Write kubeconfig.
	return writeEKSKubeconfig(ctx, cfg, ecfg.ClusterName, ecfg.Region)
}

func waitEKSClusterActive(ctx context.Context, client *eks.Client, clusterName string) error {
	waiter := eks.NewClusterActiveWaiter(client, func(o *eks.ClusterActiveWaiterOptions) {
		o.MaxDelay = 30 * time.Second
		o.MinDelay = 15 * time.Second
	})
	return waiter.Wait(ctx, &eks.DescribeClusterInput{Name: aws.String(clusterName)}, 30*time.Minute)
}

func ensureNodeGroup(ctx context.Context, client *eks.Client, ecfg *EKSConfig, nodeRoleARN string) error {
	_, err := client.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(ecfg.ClusterName),
		NodegroupName: aws.String(ecfg.NodeGroupName),
	})
	if err == nil {
		return nil // already exists
	}

	desired := ecfg.NodeDesiredCount
	if desired == 0 {
		desired = 2
	}
	minSize := ecfg.NodeMinCount
	if minSize == 0 {
		minSize = 1
	}
	maxSize := ecfg.NodeMaxCount
	if maxSize == 0 {
		maxSize = desired + 2
	}

	_, err = client.CreateNodegroup(ctx, &eks.CreateNodegroupInput{
		ClusterName:   aws.String(ecfg.ClusterName),
		NodegroupName: aws.String(ecfg.NodeGroupName),
		NodeRole:      aws.String(nodeRoleARN),
		Subnets:       ecfg.SubnetIDs,
		InstanceTypes: []string{ecfg.NodeInstanceType},
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: aws.Int32(desired),
			MinSize:     aws.Int32(minSize),
			MaxSize:     aws.Int32(maxSize),
		},
		Tags: ecfg.Tags,
	})
	if err != nil {
		return fmt.Errorf("creating EKS node group %q: %w", ecfg.NodeGroupName, err)
	}

	// Wait for node group to become ACTIVE.
	waiter := eks.NewNodegroupActiveWaiter(client, func(o *eks.NodegroupActiveWaiterOptions) {
		o.MaxDelay = 30 * time.Second
	})
	return waiter.Wait(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(ecfg.ClusterName),
		NodegroupName: aws.String(ecfg.NodeGroupName),
	}, 20*time.Minute)
}

// writeEKSKubeconfig writes the kubeconfig for the given cluster and returns
// the file path.  Uses aws eks get-token under the hood via the kubeconfig
// exec credential plugin.
func writeEKSKubeconfig(ctx context.Context, cfg aws.Config, clusterName, region string) (string, error) {
	client := eks.NewFromConfig(cfg)
	out, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(clusterName)})
	if err != nil {
		return "", fmt.Errorf("describing EKS cluster: %w", err)
	}
	cluster := out.Cluster
	endpoint := aws.ToString(cluster.Endpoint)
	caData := aws.ToString(cluster.CertificateAuthority.Data)

	kubeconfigContent := fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    server: %s
    certificate-authority-data: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: aws-eks
  name: %s
current-context: %s
kind: Config
users:
- name: aws-eks
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args:
      - eks
      - get-token
      - --cluster-name
      - %s
      - --region
      - %s
`, endpoint, caData, clusterName, clusterName, clusterName, clusterName, clusterName, region)

	home, _ := os.UserHomeDir()
	kubeconfigDir := filepath.Join(home, ".bolt", "kubeconfigs")
	if err := os.MkdirAll(kubeconfigDir, 0700); err != nil {
		return "", err
	}
	kubeconfigPath := filepath.Join(kubeconfigDir, clusterName+".yaml")
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0600); err != nil {
		return "", fmt.Errorf("writing kubeconfig: %w", err)
	}
	return kubeconfigPath, nil
}

// ensureEKSClusterRole creates (or finds) the IAM role for the EKS control plane.
func ensureEKSClusterRole(ctx context.Context, cfg aws.Config, roleName string) (string, error) {
	return ensureIAMRole(ctx, cfg, roleName,
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"eks.amazonaws.com"},"Action":"sts:AssumeRole"}]}`,
		[]string{"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"},
	)
}

// ensureEKSNodeRole creates (or finds) the IAM role for EKS worker nodes.
func ensureEKSNodeRole(ctx context.Context, cfg aws.Config, roleName string) (string, error) {
	return ensureIAMRole(ctx, cfg, roleName,
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`,
		[]string{
			"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
			"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
			"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
		},
	)
}

func ensureIAMRole(ctx context.Context, cfg aws.Config, roleName, assumeRolePolicy string, managedPolicies []string) (string, error) {
	iamClient := iam.NewFromConfig(cfg)

	// Check if role exists.
	existing, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err == nil {
		return aws.ToString(existing.Role.Arn), nil
	}

	created, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicy),
	})
	if err != nil {
		return "", fmt.Errorf("creating IAM role %q: %w", roleName, err)
	}
	roleARN := aws.ToString(created.Role.Arn)

	for _, policy := range managedPolicies {
		if _, err := iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policy),
		}); err != nil {
			return "", fmt.Errorf("attaching policy %s to %s: %w", policy, roleName, err)
		}
	}
	return roleARN, nil
}
