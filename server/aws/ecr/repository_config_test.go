package ecr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKECRPutImageTagMutability exercises the Terraform in-place update of
// aws_ecr_repository.image_tag_mutability: PutImageTagMutability was previously
// undispatched (UnknownOperationException). The new setting must be reflected by
// DescribeRepositories and, critically, take effect on subsequent pushes.
func TestSDKECRPutImageTagMutability(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("mut-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("mut-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage v1: %v", err)
	}

	// Flip to IMMUTABLE.
	put, err := client.PutImageTagMutability(ctx, &awsecr.PutImageTagMutabilityInput{
		RepositoryName:     aws.String("mut-repo"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityImmutable,
	})
	if err != nil {
		t.Fatalf("PutImageTagMutability(IMMUTABLE): %v", err)
	}

	if put.ImageTagMutability != ecrtypes.ImageTagMutabilityImmutable {
		t.Fatalf("PutImageTagMutability echoed %q, want IMMUTABLE", put.ImageTagMutability)
	}

	if aws.ToString(put.RegistryId) != "123456789012" || aws.ToString(put.RepositoryName) != "mut-repo" {
		t.Fatalf("PutImageTagMutability response registryId=%q repositoryName=%q",
			aws.ToString(put.RegistryId), aws.ToString(put.RepositoryName))
	}

	// DescribeRepositories reflects the new value.
	desc, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"mut-repo"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}

	if desc.Repositories[0].ImageTagMutability != ecrtypes.ImageTagMutabilityImmutable {
		t.Fatalf("DescribeRepositories mutability = %q, want IMMUTABLE",
			desc.Repositories[0].ImageTagMutability)
	}

	// The setting takes effect: re-pushing an existing tag now fails.
	_, err = client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("mut-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	})

	var already *ecrtypes.ImageTagAlreadyExistsException
	if !errors.As(err, &already) {
		t.Fatalf("re-push to IMMUTABLE repo: want ImageTagAlreadyExistsException, got %v", err)
	}

	// Flip back to MUTABLE; re-push succeeds again.
	if _, err := client.PutImageTagMutability(ctx, &awsecr.PutImageTagMutabilityInput{
		RepositoryName:     aws.String("mut-repo"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityMutable,
	}); err != nil {
		t.Fatalf("PutImageTagMutability(MUTABLE): %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("mut-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("re-push after MUTABLE: %v", err)
	}
}

// TestSDKECRPutImageScanningConfiguration exercises the Terraform in-place update
// of aws_ecr_repository.image_scanning_configuration.scan_on_push, previously
// undispatched.
func TestSDKECRPutImageScanningConfiguration(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	put, err := client.PutImageScanningConfiguration(ctx, &awsecr.PutImageScanningConfigurationInput{
		RepositoryName:             aws.String("scan-repo"),
		ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{ScanOnPush: true},
	})
	if err != nil {
		t.Fatalf("PutImageScanningConfiguration: %v", err)
	}

	if put.ImageScanningConfiguration == nil || !put.ImageScanningConfiguration.ScanOnPush {
		t.Fatalf("PutImageScanningConfiguration echoed %+v, want scanOnPush=true",
			put.ImageScanningConfiguration)
	}

	if aws.ToString(put.RepositoryName) != "scan-repo" {
		t.Fatalf("PutImageScanningConfiguration repositoryName = %q", aws.ToString(put.RepositoryName))
	}

	desc, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"scan-repo"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}

	cfg := desc.Repositories[0].ImageScanningConfiguration
	if cfg == nil || !cfg.ScanOnPush {
		t.Fatalf("DescribeRepositories scanning config = %+v, want scanOnPush=true", cfg)
	}
}

// TestSDKECRPutImageTagMutabilityMissingRepo asserts the operation surfaces
// RepositoryNotFoundException for an unknown repository.
func TestSDKECRPutImageTagMutabilityMissingRepo(t *testing.T) {
	client := newECRClient(t)

	_, err := client.PutImageTagMutability(context.Background(), &awsecr.PutImageTagMutabilityInput{
		RepositoryName:     aws.String("ghost-repo"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityImmutable,
	})

	var notFound *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("mutability on missing repo: want RepositoryNotFoundException, got %v", err)
	}
}
