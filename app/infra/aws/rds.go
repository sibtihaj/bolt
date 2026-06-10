// Package aws provides AWS infrastructure provisioning for bolt.
// Phase 3: RDS PostgreSQL provisioning.
package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// RDSConfig holds parameters for provisioning an RDS PostgreSQL instance.
type RDSConfig struct {
	InstanceID    string   // e.g. "bolt-prod-db"
	InstanceClass string   // e.g. "db.t3.medium"
	StorageGB     int32
	DBName        string   // database name inside PostgreSQL
	MasterUser    string
	MasterPass    string
	SubnetGroupName string
	VPCSecurityGroupIDs []string
	Region        string
	Tags          map[string]string
}

// EnsureRDSPostgres creates (or resumes) an RDS PostgreSQL instance and waits
// for it to become available.  Returns the postgres:// connection URL.
//
// Phase 3 implementation — provisions a managed PostgreSQL 15 instance.
func EnsureRDSPostgres(ctx context.Context, cfg aws.Config, rcfg *RDSConfig) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	client := rds.NewFromConfig(cfg)

	// Check whether the instance already exists.
	existing, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(rcfg.InstanceID),
	})
	var endpoint string
	if err == nil && len(existing.DBInstances) > 0 {
		db := existing.DBInstances[0]
		if db.DBInstanceStatus != nil && *db.DBInstanceStatus != "available" {
			if err := waitRDSAvailable(ctx, client, rcfg.InstanceID); err != nil {
				return "", err
			}
		}
		if db.Endpoint != nil {
			endpoint = aws.ToString(db.Endpoint.Address)
		}
	} else {
		// Create the instance.
		tags := rdsTagsFromMap(rcfg.Tags)
		createInput := &rds.CreateDBInstanceInput{
			DBInstanceIdentifier: aws.String(rcfg.InstanceID),
			DBInstanceClass:      aws.String(rcfg.InstanceClass),
			Engine:               aws.String("postgres"),
			EngineVersion:        aws.String("15"),
			DBName:               aws.String(rcfg.DBName),
			MasterUsername:       aws.String(rcfg.MasterUser),
			MasterUserPassword:   aws.String(rcfg.MasterPass),
			AllocatedStorage:     aws.Int32(rcfg.StorageGB),
			StorageType:          aws.String("gp3"),
			StorageEncrypted:     aws.Bool(true),
			MultiAZ:              aws.Bool(false),
			PubliclyAccessible:   aws.Bool(false),
			Tags:                 tags,
		}
		if rcfg.SubnetGroupName != "" {
			createInput.DBSubnetGroupName = aws.String(rcfg.SubnetGroupName)
		}
		if len(rcfg.VPCSecurityGroupIDs) > 0 {
			createInput.VpcSecurityGroupIds = rcfg.VPCSecurityGroupIDs
		}

		created, err := client.CreateDBInstance(ctx, createInput)
		if err != nil {
			return "", fmt.Errorf("creating RDS instance %q: %w", rcfg.InstanceID, err)
		}
		if created.DBInstance != nil && created.DBInstance.Endpoint != nil {
			endpoint = aws.ToString(created.DBInstance.Endpoint.Address)
		}

		if err := waitRDSAvailable(ctx, client, rcfg.InstanceID); err != nil {
			return "", err
		}

		// Re-describe to get the endpoint after availability.
		if endpoint == "" {
			desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
				DBInstanceIdentifier: aws.String(rcfg.InstanceID),
			})
			if err == nil && len(desc.DBInstances) > 0 && desc.DBInstances[0].Endpoint != nil {
				endpoint = aws.ToString(desc.DBInstances[0].Endpoint.Address)
			}
		}
	}

	if endpoint == "" {
		return "", fmt.Errorf("RDS instance %q has no endpoint after provisioning", rcfg.InstanceID)
	}

	return fmt.Sprintf("postgres://%s:%s@%s:5432/%s",
		rcfg.MasterUser, rcfg.MasterPass, endpoint, rcfg.DBName), nil
}

// DeleteRDSPostgres deletes the RDS instance without a final snapshot.
// Safe to call if the instance does not exist.
func DeleteRDSPostgres(ctx context.Context, cfg aws.Config, instanceID string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	client := rds.NewFromConfig(cfg)
	_, err := client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		SkipFinalSnapshot:    aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("deleting RDS instance %q: %w", instanceID, err)
	}
	return nil
}

func waitRDSAvailable(ctx context.Context, client *rds.Client, instanceID string) error {
	waiter := rds.NewDBInstanceAvailableWaiter(client, func(o *rds.DBInstanceAvailableWaiterOptions) {
		o.MaxDelay = 30 * time.Second
		o.MinDelay = 10 * time.Second
	})
	return waiter.Wait(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceID),
	}, 30*time.Minute)
}

func rdsTagsFromMap(m map[string]string) []types.Tag {
	tags := make([]types.Tag, 0, len(m))
	for k, v := range m {
		k, v := k, v
		tags = append(tags, types.Tag{Key: &k, Value: &v})
	}
	return tags
}
