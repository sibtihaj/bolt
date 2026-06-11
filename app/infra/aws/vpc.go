// Phase 4: VPC provisioning for EKS.
package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	"github.com/sibtihaj/bolt/app/infra/errs"
)

// VpcLimitExceededError is returned when the AWS account has reached its VPC
// quota. It carries the AWS config so the caller can list / delete VPCs.
type VpcLimitExceededError struct {
	Config aws.Config
	Cause  error
}

func (e *VpcLimitExceededError) Error() string    { return e.Cause.Error() }
func (e *VpcLimitExceededError) Unwrap() error    { return e.Cause }
func (e *VpcLimitExceededError) Kind() errs.ErrorKind { return errs.KindQuota }
func (e *VpcLimitExceededError) Resource() string     { return "VPC" }

// VPCInfo is a summary of a single VPC for display in the healing picker.
type VPCInfo struct {
	VPCID   string
	Name    string
	CIDR    string
	State   string
	Default bool
	Subnets int
}

// Label returns a human-friendly one-liner for the huh selector.
func (v VPCInfo) Label() string {
	name := v.Name
	if name == "" {
		name = "(unnamed)"
	}
	tag := ""
	if v.Default {
		tag = "  [default]"
	}
	return fmt.Sprintf("%-22s  %-28s  %s  (%d subnets)%s",
		v.VPCID, name, v.CIDR, v.Subnets, tag)
}

// ListVPCs returns all VPCs in the region, enriched with subnet counts.
func ListVPCs(ctx context.Context, cfg aws.Config) ([]VPCInfo, error) {
	client := ec2.NewFromConfig(cfg)
	vpcOut, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing VPCs: %w", err)
	}

	// Fetch subnet counts per VPC in one call.
	subOut, _ := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	subnetCount := map[string]int{}
	if subOut != nil {
		for _, s := range subOut.Subnets {
			subnetCount[aws.ToString(s.VpcId)]++
		}
	}

	result := make([]VPCInfo, 0, len(vpcOut.Vpcs))
	for _, v := range vpcOut.Vpcs {
		id := aws.ToString(v.VpcId)
		info := VPCInfo{
			VPCID:   id,
			CIDR:    aws.ToString(v.CidrBlock),
			State:   string(v.State),
			Default: aws.ToBool(v.IsDefault),
			Subnets: subnetCount[id],
		}
		for _, t := range v.Tags {
			if aws.ToString(t.Key) == "Name" {
				info.Name = aws.ToString(t.Value)
			}
		}
		result = append(result, info)
	}
	return result, nil
}

// AdoptVPC returns VPCOutputs for an existing VPC the caller wants to reuse.
// It collects the VPC's subnets (guessing public vs private by MapPublicIpOnLaunch)
// and the first non-default security group, creating one if none exists.
func AdoptVPC(ctx context.Context, cfg aws.Config, vpcID, namePrefix string) (*VPCOutputs, error) {
	client := ec2.NewFromConfig(cfg)
	subOut, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		return nil, fmt.Errorf("describing subnets for VPC %s: %w", vpcID, err)
	}

	var pub, priv []string
	for _, s := range subOut.Subnets {
		if aws.ToBool(s.MapPublicIpOnLaunch) {
			pub = append(pub, aws.ToString(s.SubnetId))
		} else {
			priv = append(priv, aws.ToString(s.SubnetId))
		}
	}
	// If the VPC has no subnets at all, fall back to using all as "public".
	if len(pub) == 0 && len(priv) == 0 {
		return nil, fmt.Errorf("VPC %s has no subnets — cannot adopt it for EKS", vpcID)
	}

	// Find or create a security group for EKS.
	sgOut, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	sgID := ""
	if err == nil {
		for _, sg := range sgOut.SecurityGroups {
			if aws.ToString(sg.GroupName) != "default" {
				sgID = aws.ToString(sg.GroupId)
				break
			}
		}
	}
	if sgID == "" {
		newSG, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(namePrefix + "-eks-sg"),
			Description: aws.String("bolt-managed EKS node security group"),
			VpcId:       aws.String(vpcID),
		})
		if err != nil {
			return nil, fmt.Errorf("creating security group in adopted VPC: %w", err)
		}
		sgID = aws.ToString(newSG.GroupId)
	}

	return &VPCOutputs{
		VPCID:            vpcID,
		PublicSubnetIDs:  pub,
		PrivateSubnetIDs: priv,
		SecurityGroupID:  sgID,
	}, nil
}

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
	var vpcOut *ec2.CreateVpcOutput
	if createErr := errs.Do(ctx, 5, func() error {
		var callErr error
		vpcOut, callErr = client.CreateVpc(ctx, &ec2.CreateVpcInput{
			CidrBlock: aws.String("10.0.0.0/16"),
			TagSpecifications: []types.TagSpecification{{
				ResourceType: types.ResourceTypeVpc,
				Tags:         ec2TagsFromMap(mergeTags(tags, map[string]string{"Name": namePrefix + "-vpc", "bolt:managed": "true"})),
			}},
		})
		return callErr
	}, nil); createErr != nil {
		var apiErr smithy.APIError
		if errors.As(createErr, &apiErr) && apiErr.ErrorCode() == "VpcLimitExceeded" {
			return nil, &VpcLimitExceededError{Config: cfg, Cause: createErr}
		}
		return nil, fmt.Errorf("creating VPC: %w", createErr)
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

// VPCValidationError is returned by ValidateVPCForEKS when the chosen VPC
// does not meet EKS requirements.  The cmd layer catches it to re-present
// the VPC picker with the failure reason instead of dropping to the main menu.
type VPCValidationError struct {
	VPCID  string
	Detail string
	Cause  error
}

func (e *VPCValidationError) Error() string        { return e.Cause.Error() }
func (e *VPCValidationError) Unwrap() error        { return e.Cause }
func (e *VPCValidationError) Kind() errs.ErrorKind { return errs.KindConfig }
func (e *VPCValidationError) Resource() string     { return "VPC" }

// ValidateVPCForEKS checks whether an existing VPC has the capacity and
// networking prerequisites to host an EKS cluster with nodeCount worker nodes.
//
// Checks performed:
//   - At least 2 subnets exist and span at least 2 availability zones (EKS hard requirement)
//   - Enough free IP addresses for EKS ENI pre-allocation (nodeCount×30 + 30 headroom)
//   - An internet gateway is attached to the VPC (required for node image pulls)
//   - Public subnets have a 0.0.0.0/0 route to that IGW
//   - Private subnets (if any) have a 0.0.0.0/0 route to a NAT gateway
func ValidateVPCForEKS(ctx context.Context, cfg aws.Config, vpcID string, out *VPCOutputs, nodeCount int) error {
	client := ec2.NewFromConfig(cfg)

	// vErr wraps a validation failure as a VPCValidationError so the cmd layer
	// can loop back to the picker instead of dropping to the main menu.
	vErr := func(msg string, args ...interface{}) error {
		cause := fmt.Errorf(msg, args...)
		return &VPCValidationError{VPCID: vpcID, Detail: cause.Error(), Cause: cause}
	}

	allSubnetIDs := append(out.PublicSubnetIDs, out.PrivateSubnetIDs...)
	if len(allSubnetIDs) == 0 {
		return vErr("VPC %s has no subnets — EKS requires at least 2 subnets in 2 different AZs", vpcID)
	}

	subOut, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: allSubnetIDs,
	})
	if err != nil {
		return fmt.Errorf("describing subnets for VPC %s: %w", vpcID, err)
	}

	// ── 1. AZ diversity ──────────────────────────────────────────────────────
	azSet := map[string]struct{}{}
	for _, s := range subOut.Subnets {
		azSet[aws.ToString(s.AvailabilityZone)] = struct{}{}
	}
	if len(azSet) < 2 {
		names := make([]string, 0, len(azSet))
		for az := range azSet {
			names = append(names, az)
		}
		return vErr("VPC %s subnets only span %d availability zone (%s); EKS requires at least 2 — "+
			"add subnets in a second AZ or pick a different VPC", vpcID, len(azSet), names[0])
	}

	// ── 2. Free IP addresses ─────────────────────────────────────────────────
	required := (nodeCount * 30) + 30
	totalFree := 0
	for _, s := range subOut.Subnets {
		totalFree += int(aws.ToInt32(s.AvailableIpAddressCount))
	}
	if totalFree < required {
		return vErr("VPC %s has only %d free IP addresses across all subnets; "+
			"need at least %d for %d nodes (EKS ENI pre-allocation) — "+
			"delete unused resources or pick a VPC with larger subnets",
			vpcID, totalFree, required, nodeCount)
	}

	// ── 3. Internet Gateway attached ─────────────────────────────────────────
	igwOut, err := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []types.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("attachment.state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return fmt.Errorf("checking internet gateway for VPC %s: %w", vpcID, err)
	}
	if len(igwOut.InternetGateways) == 0 {
		return vErr("VPC %s has no internet gateway attached — "+
			"EKS nodes need an IGW to pull container images; "+
			"attach one or pick a VPC that already has one", vpcID)
	}
	igwID := aws.ToString(igwOut.InternetGateways[0].InternetGatewayId)

	// ── 4. Route tables ───────────────────────────────────────────────────────
	rtOut, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return fmt.Errorf("describing route tables for VPC %s: %w", vpcID, err)
	}

	subnetRT := map[string]*ec2RouteTable{}
	var mainRT *ec2RouteTable
	for i := range rtOut.RouteTables {
		rt := &ec2RouteTable{routes: rtOut.RouteTables[i].Routes}
		for _, assoc := range rtOut.RouteTables[i].Associations {
			if aws.ToBool(assoc.Main) {
				mainRT = rt
			}
			if assoc.SubnetId != nil {
				subnetRT[aws.ToString(assoc.SubnetId)] = rt
			}
		}
	}
	resolveRT := func(subnetID string) *ec2RouteTable {
		if rt, ok := subnetRT[subnetID]; ok {
			return rt
		}
		return mainRT
	}

	for _, subID := range out.PublicSubnetIDs {
		rt := resolveRT(subID)
		if rt == nil || !rt.hasRoute("0.0.0.0/0", "igw-", igwID) {
			return vErr("public subnet %s in VPC %s has no default route to internet gateway %s — "+
				"nodes on this subnet will not be able to pull container images",
				subID, vpcID, igwID)
		}
	}

	for _, subID := range out.PrivateSubnetIDs {
		rt := resolveRT(subID)
		if rt == nil || !rt.hasNATRoute() {
			return vErr("private subnet %s in VPC %s has no default route to a NAT gateway — "+
				"EKS nodes on this subnet will not be able to reach AWS APIs",
				subID, vpcID)
		}
	}

	return nil
}

// ec2RouteTable is a thin wrapper so hasRoute / hasNATRoute stay readable.
type ec2RouteTable struct {
	routes []types.Route
}

func (rt *ec2RouteTable) hasRoute(cidr, gatewayPrefix, exactGatewayID string) bool {
	for _, r := range rt.routes {
		if aws.ToString(r.DestinationCidrBlock) != cidr {
			continue
		}
		gw := aws.ToString(r.GatewayId)
		if (gatewayPrefix != "" && contains(gw, gatewayPrefix)) ||
			(exactGatewayID != "" && gw == exactGatewayID) {
			return true
		}
	}
	return false
}

func (rt *ec2RouteTable) hasNATRoute() bool {
	for _, r := range rt.routes {
		if aws.ToString(r.DestinationCidrBlock) == "0.0.0.0/0" &&
			r.NatGatewayId != nil && aws.ToString(r.NatGatewayId) != "" {
			return true
		}
	}
	return false
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
