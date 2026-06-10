package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// EnsureS3Bucket creates the bucket if it does not already exist and enables
// versioning.  Returns the bucket name.
// Region must already be set in the provided aws.Config.
func EnsureS3Bucket(ctx context.Context, cfg aws.Config, bucketName, region string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	client := s3.NewFromConfig(cfg)

	// Check whether the bucket already exists and is ours.
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucketName)})
	if err == nil {
		// Bucket exists — enable versioning in case it was disabled.
		return bucketName, enableVersioning(ctx, client, bucketName)
	}

	var httpErr *smithyhttp.ResponseError
	if !errors.As(err, &httpErr) || httpErr.HTTPStatusCode() != 404 {
		return "", fmt.Errorf("checking S3 bucket %q: %w", bucketName, err)
	}

	// Create the bucket.
	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}
	// us-east-1 is the default region and must NOT specify a LocationConstraint.
	if region != "" && region != "us-east-1" {
		createInput.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(region),
		}
	}

	if _, err := client.CreateBucket(ctx, createInput); err != nil {
		return "", fmt.Errorf("creating S3 bucket %q in %s: %w", bucketName, region, err)
	}

	// Block all public access.
	if _, err := client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucketName),
		PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			IgnorePublicAcls:      aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(true),
		},
	}); err != nil {
		return "", fmt.Errorf("blocking public access on %q: %w", bucketName, err)
	}

	// Enable server-side encryption (AES-256).
	if _, err := client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: aws.String(bucketName),
		ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{
			Rules: []types.ServerSideEncryptionRule{{
				ApplyServerSideEncryptionByDefault: &types.ServerSideEncryptionByDefault{
					SSEAlgorithm: types.ServerSideEncryptionAes256,
				},
				BucketKeyEnabled: aws.Bool(true),
			}},
		},
	}); err != nil {
		return "", fmt.Errorf("enabling encryption on %q: %w", bucketName, err)
	}

	if err := enableVersioning(ctx, client, bucketName); err != nil {
		return "", err
	}

	return bucketName, nil
}

// DeleteS3Bucket empties and deletes the bucket.  Safe to call if the bucket
// does not exist (404 is silently ignored).
func DeleteS3Bucket(ctx context.Context, cfg aws.Config, bucketName string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	client := s3.NewFromConfig(cfg)

	// Empty the bucket first (required before deletion).
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			var httpErr *smithyhttp.ResponseError
			if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 404 {
				return nil // already gone
			}
			return fmt.Errorf("listing objects in %q: %w", bucketName, err)
		}
		objects := make([]types.ObjectIdentifier, len(page.Contents))
		for i, obj := range page.Contents {
			objects[i] = types.ObjectIdentifier{Key: obj.Key}
		}
		if len(objects) > 0 {
			if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucketName),
				Delete: &types.Delete{Objects: objects},
			}); err != nil {
				return fmt.Errorf("deleting objects from %q: %w", bucketName, err)
			}
		}
	}

	if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	}); err != nil {
		var httpErr *smithyhttp.ResponseError
		if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 404 {
			return nil
		}
		return fmt.Errorf("deleting S3 bucket %q: %w", bucketName, err)
	}
	return nil
}

func enableVersioning(ctx context.Context, client *s3.Client, bucketName string) error {
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	if err != nil {
		return fmt.Errorf("enabling versioning on %q: %w", bucketName, err)
	}
	return nil
}
