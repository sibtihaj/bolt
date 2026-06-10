// EKS cluster and VPC teardown for bolt destroy.
package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// DeleteEKSCluster deletes the managed node groups for a cluster, then the
// cluster itself, and waits for full deletion.
func DeleteEKSCluster(ctx context.Context, cfg aws.Config, clusterName string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	client := eks.NewFromConfig(cfg)

	// List and delete all node groups first.
	ngOut, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
		ClusterName: aws.String(clusterName),
	})
	if err != nil {
		return fmt.Errorf("listing EKS node groups: %w", err)
	}
	for _, ng := range ngOut.Nodegroups {
		ng := ng
		if _, err := client.DeleteNodegroup(ctx, &eks.DeleteNodegroupInput{
			ClusterName:   aws.String(clusterName),
			NodegroupName: aws.String(ng),
		}); err != nil {
			return fmt.Errorf("deleting node group %q: %w", ng, err)
		}
	}

	// Wait for all node groups to be deleted.
	for _, ng := range ngOut.Nodegroups {
		ng := ng
		waiter := eks.NewNodegroupDeletedWaiter(client, func(o *eks.NodegroupDeletedWaiterOptions) {
			o.MaxDelay = 30 * time.Second
		})
		if err := waiter.Wait(ctx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(clusterName),
			NodegroupName: aws.String(ng),
		}, 20*time.Minute); err != nil {
			return fmt.Errorf("waiting for node group %q deletion: %w", ng, err)
		}
	}

	// Delete the cluster.
	if _, err := client.DeleteCluster(ctx, &eks.DeleteClusterInput{
		Name: aws.String(clusterName),
	}); err != nil {
		var httpErr *smithyhttp.ResponseError
		if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 404 {
			return nil // already gone
		}
		return fmt.Errorf("deleting EKS cluster %q: %w", clusterName, err)
	}

	waiter := eks.NewClusterDeletedWaiter(client, func(o *eks.ClusterDeletedWaiterOptions) {
		o.MaxDelay = 30 * time.Second
	})
	return waiter.Wait(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	}, 20*time.Minute)
}

// DeleteVPC removes all bolt-managed subnets, route tables, IGW, security
// groups, and finally the VPC itself.  Safe to call if the VPC no longer exists.
func DeleteVPC(ctx context.Context, cfg aws.Config, vpcID string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	client := ec2.NewFromConfig(cfg)

	// 1. Detach and delete Internet Gateways.
	igwOut, err := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []types.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}},
		},
	})
	if err == nil {
		for _, igw := range igwOut.InternetGateways {
			igwID := aws.ToString(igw.InternetGatewayId)
			client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{ //nolint:errcheck
				InternetGatewayId: aws.String(igwID),
				VpcId:             aws.String(vpcID),
			})
			if _, err := client.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
				InternetGatewayId: aws.String(igwID),
			}); err != nil {
				return fmt.Errorf("deleting internet gateway %s: %w", igwID, err)
			}
		}
	}

	// 2. Delete subnets.
	subOut, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err == nil {
		for _, sub := range subOut.Subnets {
			if _, err := client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
				SubnetId: aws.String(aws.ToString(sub.SubnetId)),
			}); err != nil {
				return fmt.Errorf("deleting subnet: %w", err)
			}
		}
	}

	// 3. Delete non-main route tables.
	rtOut, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err == nil {
		for _, rt := range rtOut.RouteTables {
			// Skip the main route table (it's deleted with the VPC).
			isMain := false
			for _, assoc := range rt.Associations {
				if aws.ToBool(assoc.Main) {
					isMain = true
					break
				}
			}
			if isMain {
				continue
			}
			client.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{ //nolint:errcheck
				RouteTableId: rt.RouteTableId,
			})
		}
	}

	// 4. Delete security groups (skip the default one).
	sgOut, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err == nil {
		for _, sg := range sgOut.SecurityGroups {
			if aws.ToString(sg.GroupName) == "default" {
				continue
			}
			client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{ //nolint:errcheck
				GroupId: sg.GroupId,
			})
		}
	}

	// 5. Finally delete the VPC itself.
	if _, err := client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	}); err != nil {
		var httpErr *smithyhttp.ResponseError
		if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 404 {
			return nil
		}
		return fmt.Errorf("deleting VPC %s: %w", vpcID, err)
	}
	return nil
}
