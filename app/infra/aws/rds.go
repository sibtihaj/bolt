// Package aws provides AWS infrastructure provisioning for bolt.
// Phase 3: RDS PostgreSQL provisioning.
package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"
	"github.com/sibtihaj/bolt/app/infra/errs"
)

// RDSQuotaError is returned when the account RDS instance quota is exhausted.
type RDSQuotaError struct {
	Config aws.Config
	Cause  error
}

func (e *RDSQuotaError) Error() string        { return e.Cause.Error() }
func (e *RDSQuotaError) Unwrap() error        { return e.Cause }
func (e *RDSQuotaError) Kind() errs.ErrorKind { return errs.KindQuota }
func (e *RDSQuotaError) Resource() string     { return "RDS instance" }

// RDSCapacityError is returned when the requested instance class has no
// available capacity in the target AZ.
type RDSCapacityError struct {
	Config        aws.Config
	InstanceClass string
	Cause         error
}

func (e *RDSCapacityError) Error() string        { return e.Cause.Error() }
func (e *RDSCapacityError) Unwrap() error        { return e.Cause }
func (e *RDSCapacityError) Kind() errs.ErrorKind { return errs.KindCapacity }
func (e *RDSCapacityError) Resource() string     { return "RDS instance" }

// RDSInfo is a summary of an existing RDS instance for display in the heal picker.
type RDSInfo struct {
	InstanceID    string
	InstanceClass string
	Status        string
	Engine        string
	MultiAZ       bool
}

// Label returns a human-friendly one-liner for the huh selector.
func (r RDSInfo) Label() string {
	multiAZ := ""
	if r.MultiAZ {
		multiAZ = "  multi-az"
	}
	return fmt.Sprintf("%-32s  %-16s  %-12s  %s%s",
		r.InstanceID, r.InstanceClass, r.Status, r.Engine, multiAZ)
}

// ListRDSInstances returns a summary of every RDS instance in the account/region.
func ListRDSInstances(ctx context.Context, cfg aws.Config) ([]RDSInfo, error) {
	client := rds.NewFromConfig(cfg)
	out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("listing RDS instances: %w", err)
	}
	result := make([]RDSInfo, 0, len(out.DBInstances))
	for _, db := range out.DBInstances {
		result = append(result, RDSInfo{
			InstanceID:    aws.ToString(db.DBInstanceIdentifier),
			InstanceClass: aws.ToString(db.DBInstanceClass),
			Status:        aws.ToString(db.DBInstanceStatus),
			Engine:        aws.ToString(db.Engine) + " " + aws.ToString(db.EngineVersion),
			MultiAZ:       aws.ToBool(db.MultiAZ),
		})
	}
	return result, nil
}

// RDSInstanceStatus returns the current status of an RDS instance
// (e.g. creating, available, backing-up).  Returns "unknown" on error.
func RDSInstanceStatus(ctx context.Context, cfg aws.Config, instanceID string) string {
	client := rds.NewFromConfig(cfg)
	out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceID),
	})
	if err != nil || len(out.DBInstances) == 0 {
		return "unknown"
	}
	return aws.ToString(out.DBInstances[0].DBInstanceStatus)
}

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

		var created *rds.CreateDBInstanceOutput
		if createErr := errs.Do(ctx, 5, func() error {
			var callErr error
			created, callErr = client.CreateDBInstance(ctx, createInput)
			return callErr
		}, nil); createErr != nil {
			var apiErr smithy.APIError
			if errors.As(createErr, &apiErr) {
				switch apiErr.ErrorCode() {
				case "InstanceQuotaExceededFault", "DBParameterGroupQuotaExceeded",
					"DBSubnetGroupQuotaExceeded", "StorageQuotaExceededFault":
					return "", &RDSQuotaError{Config: cfg, Cause: createErr}
				case "InsufficientDBInstanceCapacityFault":
					return "", &RDSCapacityError{Config: cfg, InstanceClass: rcfg.InstanceClass, Cause: createErr}
				}
			}
			return "", fmt.Errorf("creating RDS instance %q: %w", rcfg.InstanceID, createErr)
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
