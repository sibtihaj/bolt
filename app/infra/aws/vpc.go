// Phase 4: VPC provisioning for EKS.
package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// VPCOutputs holds IDs of all VPC resources bolt created.
type VPCOutputs struct {
	VPCID          string
	PublicSubnetIDs  []string
	PrivateSubnetIDs []string
	SecurityGroupID  string
}

// EnsureVPC creates a VPC with two public and two private subnets across the
// first two available AZs in the region.  Idempotent — if a VPC tagged with
// namePrefix already exists, its outputs are returned.
//
// Phase 4 implementation.
func EnsureVPC(ctx context.Context, cfg aws.Config, namePrefix, region string, tags map[string]string) (*VPCOutputs, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	client := ec2.NewFromConfig(cfg)

	// Check whether our VPC already exists.
	existing, err := findVPCByTag(ctx, client, namePrefix)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	// Create VPC (10.0.0.0/16).
	vpcOut, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeVpc,
			Tags:         ec2TagsFromMap(mergeTags(tags, map[string]string{"Name": namePrefix + "-vpc", "bolt:managed": "true"})),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("creating VPC: %w", err)
	}
	vpcID := aws.ToString(vpcOut.Vpc.VpcId)

	// Enable DNS hostnames (required for EKS).
	if _, err := client.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{
		VpcId: aws.String(vpcID),
		EnableDnsHostnames: &types.AttributeBooleanValue{Value: aws.Bool(true)},
	}); err != nil {
		return nil, fmt.Errorf("enabling DNS hostnames on VPC: %w", err)
	}

	// Get first two AZs.
	azOut, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{
		Filters: []types.Filter{{Name: aws.String("state"), Values: []string{"available"}}},
	})
	if err != nil || len(azOut.AvailabilityZones) < 2 {
		return nil, fmt.Errorf("need at least 2 availability zones, got: %v", err)
	}
	az1 := aws.ToString(azOut.AvailabilityZones[0].ZoneName)
	az2 := aws.ToString(azOut.AvailabilityZones[1].ZoneName)

	// Create subnets.
	pubSub1, err := createSubnet(ctx, client, vpcID, "10.0.1.0/24", az1, namePrefix+"-pub-1", true, tags)
	if err != nil {
		return nil, err
	}
	pubSub2, err := createSubnet(ctx, client, vpcID, "10.0.2.0/24", az2, namePrefix+"-pub-2", true, tags)
	if err != nil {
		return nil, err
	}
	privSub1, err := createSubnet(ctx, client, vpcID, "10.0.3.0/24", az1, namePrefix+"-priv-1", false, tags)
	if err != nil {
		return nil, err
	}
	privSub2, err := createSubnet(ctx, client, vpcID, "10.0.4.0/24", az2, namePrefix+"-priv-2", false, tags)
	if err != nil {
		return nil, err
	}

	// Internet Gateway for public subnets.
	igwOut, err := client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeInternetGateway,
			Tags:         ec2TagsFromMap(mergeTags(tags, map[string]string{"Name": namePrefix + "-igw", "bolt:managed": "true"})),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("creating internet gateway: %w", err)
	}
	igwID := aws.ToString(igwOut.InternetGateway.InternetGatewayId)

	if _, err := client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		VpcId: aws.String(vpcID), InternetGatewayId: aws.String(igwID),
	}); err != nil {
		return nil, fmt.Errorf("attaching internet gateway: %w", err)
	}

	// Public route table → IGW.
	pubRTOut, err := client.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: aws.String(vpcID)})
	if err != nil {
		return nil, fmt.Errorf("creating public route table: %w", err)
	}
	pubRT := aws.ToString(pubRTOut.RouteTable.RouteTableId)

	if _, err := client.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(pubRT),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwID),
	}); err != nil {
		return nil, fmt.Errorf("creating public route: %w", err)
	}

	for _, subID := range []string{pubSub1, pubSub2} {
		if _, err := client.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
			RouteTableId: aws.String(pubRT), SubnetId: aws.String(subID),
		}); err != nil {
			return nil, fmt.Errorf("associating subnet %s with public route table: %w", subID, err)
		}
	}

	// Security group for EKS nodes.
	sgOut, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(namePrefix + "-eks-sg"),
		Description: aws.String("bolt-managed EKS node security group"),
		VpcId:       aws.String(vpcID),
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeSecurityGroup,
			Tags:         ec2TagsFromMap(mergeTags(tags, map[string]string{"Name": namePrefix + "-eks-sg", "bolt:managed": "true"})),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("creating security group: %w", err)
	}
	sgID := aws.ToString(sgOut.GroupId)

	return &VPCOutputs{
		VPCID:            vpcID,
		PublicSubnetIDs:  []string{pubSub1, pubSub2},
		PrivateSubnetIDs: []string{privSub1, privSub2},
		SecurityGroupID:  sgID,
	}, nil
}

func findVPCByTag(ctx context.Context, client *ec2.Client, namePrefix string) (*VPCOutputs, error) {
	out, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []types.Filter{
			{Name: aws.String("tag:bolt:managed"), Values: []string{"true"}},
			{Name: aws.String("tag:Name"), Values: []string{namePrefix + "-vpc"}},
		},
	})
	if err != nil || len(out.Vpcs) == 0 {
		return nil, err
	}
	// VPC found — collect subnets.
	vpcID := aws.ToString(out.Vpcs[0].VpcId)
	subOut, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return nil, err
	}
	var pub, priv []string
	for _, s := range subOut.Subnets {
		for _, t := range s.Tags {
			if aws.ToString(t.Key) == "Name" {
				if contains(aws.ToString(t.Value), "-pub-") {
					pub = append(pub, aws.ToString(s.SubnetId))
				} else {
					priv = append(priv, aws.ToString(s.SubnetId))
				}
			}
		}
	}
	return &VPCOutputs{VPCID: vpcID, PublicSubnetIDs: pub, PrivateSubnetIDs: priv}, nil
}

func createSubnet(ctx context.Context, client *ec2.Client, vpcID, cidr, az, name string, mapPublicIP bool, tags map[string]string) (string, error) {
	out, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String(cidr),
		AvailabilityZone: aws.String(az),
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeSubnet,
			Tags:         ec2TagsFromMap(mergeTags(tags, map[string]string{"Name": name, "bolt:managed": "true"})),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("creating subnet %s: %w", name, err)
	}
	subID := aws.ToString(out.Subnet.SubnetId)
	if mapPublicIP {
		client.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{ //nolint:errcheck
			SubnetId: aws.String(subID),
			MapPublicIpOnLaunch: &types.AttributeBooleanValue{Value: aws.Bool(true)},
		})
	}
	return subID, nil
}

func ec2TagsFromMap(m map[string]string) []types.Tag {
	tags := make([]types.Tag, 0, len(m))
	for k, v := range m {
		k, v := k, v
		tags = append(tags, types.Tag{Key: &k, Value: &v})
	}
	return tags
}

func mergeTags(base, extra map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
