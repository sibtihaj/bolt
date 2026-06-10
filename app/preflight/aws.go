package preflight

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// AWSIdentity summarises the resolved identity from STS GetCallerIdentity.
type AWSIdentity struct {
	AccountID string
	ARN       string
	UserID    string
}

// AWSConfig holds raw credential inputs collected by the wizard.
// Secrets are never written to disk.
type AWSConfig struct {
	// AssumeRoleARN — if non-empty, bolt assumes this role in the customer account.
	// This is the recommended enterprise pattern.
	AssumeRoleARN string
	Region        string
	// Static credentials — only used when AssumeRoleARN is empty and
	// no ambient credentials (env vars, ~/.aws/credentials) exist.
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// ValidateAWSCredentials resolves the credential chain, calls STS
// GetCallerIdentity, and returns the resolved identity.
// Returns an error if the credentials are invalid or lack sts:GetCallerIdentity.
func ValidateAWSCredentials(cfg *AWSConfig) (*AWSIdentity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	awsCfg, err := buildAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("building AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(awsCfg)
	out, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("AWS credential validation failed (sts:GetCallerIdentity): %w", err)
	}

	return &AWSIdentity{
		AccountID: aws.ToString(out.Account),
		ARN:       aws.ToString(out.Arn),
		UserID:    aws.ToString(out.UserId),
	}, nil
}

// BuildAWSConfig is exported for use by infra provisioners.
func BuildAWSConfig(ctx context.Context, cfg *AWSConfig) (aws.Config, error) {
	return buildAWSConfig(ctx, cfg)
}

func buildAWSConfig(ctx context.Context, cfg *AWSConfig) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}

	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID,
				cfg.SecretAccessKey,
				cfg.SessionToken,
			),
		))
	}

	base, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("loading AWS config: %w", err)
	}

	if cfg.AssumeRoleARN != "" {
		stsClient := sts.NewFromConfig(base)
		roleProvider := stscreds.NewAssumeRoleProvider(stsClient, cfg.AssumeRoleARN)
		base.Credentials = aws.NewCredentialsCache(roleProvider)
	}

	return base, nil
}
