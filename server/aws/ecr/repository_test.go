package ecr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKECRRepositoryIdentityFields guards the identity/config fields Terraform
// and IAM tooling read: repositoryArn, registryId, imageTagMutability, and
// imageScanningConfiguration must survive both create and describe.
func TestSDKECRRepositoryIdentityFields(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	created, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName:             aws.String("ident"),
		ImageTagMutability:         ecrtypes.ImageTagMutabilityImmutable,
		ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{ScanOnPush: true},
	})
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	assertRepoFields(t, "create", created.Repository)

	desc, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"ident"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}

	if len(desc.Repositories) != 1 {
		t.Fatalf("got %d repos, want 1", len(desc.Repositories))
	}

	assertRepoFields(t, "describe", &desc.Repositories[0])
}

func assertRepoFields(t *testing.T, phase string, repo *ecrtypes.Repository) {
	t.Helper()

	if aws.ToString(repo.RepositoryArn) == "" {
		t.Fatalf("%s: repositoryArn empty", phase)
	}

	if aws.ToString(repo.RegistryId) == "" {
		t.Fatalf("%s: registryId empty", phase)
	}

	if repo.ImageTagMutability != ecrtypes.ImageTagMutabilityImmutable {
		t.Fatalf("%s: imageTagMutability = %q, want IMMUTABLE", phase, repo.ImageTagMutability)
	}

	if repo.ImageScanningConfiguration == nil || !repo.ImageScanningConfiguration.ScanOnPush {
		t.Fatalf("%s: imageScanningConfiguration missing/ScanOnPush=false: %+v", phase, repo.ImageScanningConfiguration)
	}
}

// TestSDKECRBatchGetImage guards that BatchGetImage is dispatched and returns
// the stored manifest for a resolved tag.
func TestSDKECRBatchGetImage(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("bgi"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("bgi"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	got, err := client.BatchGetImage(ctx, &awsecr.BatchGetImageInput{
		RepositoryName: aws.String("bgi"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("v1")}},
	})
	if err != nil {
		t.Fatalf("BatchGetImage: %v", err)
	}

	if len(got.Images) != 1 || len(got.Failures) != 0 {
		t.Fatalf("BatchGetImage returned images=%+v failures=%+v", got.Images, got.Failures)
	}

	img := got.Images[0]
	if aws.ToString(img.ImageManifest) != sampleManifest {
		t.Fatalf("manifest = %q, want %q", aws.ToString(img.ImageManifest), sampleManifest)
	}

	if aws.ToString(img.ImageId.ImageTag) != "v1" || aws.ToString(img.ImageId.ImageDigest) == "" {
		t.Fatalf("imageId = %+v", img.ImageId)
	}

	// A missing tag resolves to a per-image failure, not a thrown error.
	miss, err := client.BatchGetImage(ctx, &awsecr.BatchGetImageInput{
		RepositoryName: aws.String("bgi"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("ghost")}},
	})
	if err != nil {
		t.Fatalf("BatchGetImage(ghost): %v", err)
	}

	if len(miss.Failures) != 1 {
		t.Fatalf("want one failure, got %+v", miss.Failures)
	}
}

// TestSDKECRDescribeImagesNotFound guards that DescribeImages with an
// unmatched imageId throws ImageNotFoundException instead of returning empty.
func TestSDKECRDescribeImagesNotFound(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("din"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("din"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	_, err := client.DescribeImages(ctx, &awsecr.DescribeImagesInput{
		RepositoryName: aws.String("din"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("nope")}},
	})

	var notFound *ecrtypes.ImageNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("describe unmatched image: want ImageNotFoundException, got %v", err)
	}
}

// TestSDKECRImmutableRepushError guards that re-pushing an existing tag to an
// IMMUTABLE repository returns ImageTagAlreadyExistsException.
func TestSDKECRImmutableRepushError(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName:     aws.String("immut"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityImmutable,
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("immut"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage(first): %v", err)
	}

	_, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("immut"),
		ImageManifest:  aws.String(sampleManifest + " "),
		ImageTag:       aws.String("v1"),
	})

	var already *ecrtypes.ImageTagAlreadyExistsException
	if !errors.As(err, &already) {
		t.Fatalf("re-push immutable tag: want ImageTagAlreadyExistsException, got %v", err)
	}
}

// TestSDKECRPolicyNotFoundErrors guards the policy-specific not-found codes.
func TestSDKECRPolicyNotFoundErrors(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("pol"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.GetRepositoryPolicy(ctx, &awsecr.GetRepositoryPolicyInput{
		RepositoryName: aws.String("pol"),
	})

	var repoPolNF *ecrtypes.RepositoryPolicyNotFoundException
	if !errors.As(err, &repoPolNF) {
		t.Fatalf("GetRepositoryPolicy: want RepositoryPolicyNotFoundException, got %v", err)
	}

	_, err = client.GetLifecyclePolicy(ctx, &awsecr.GetLifecyclePolicyInput{
		RepositoryName: aws.String("pol"),
	})

	var lifeNF *ecrtypes.LifecyclePolicyNotFoundException
	if !errors.As(err, &lifeNF) {
		t.Fatalf("GetLifecyclePolicy: want LifecyclePolicyNotFoundException, got %v", err)
	}

	// A missing repository still reports RepositoryNotFoundException.
	_, err = client.GetRepositoryPolicy(ctx, &awsecr.GetRepositoryPolicyInput{
		RepositoryName: aws.String("ghost"),
	})

	var repoNF *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &repoNF) {
		t.Fatalf("GetRepositoryPolicy(ghost): want RepositoryNotFoundException, got %v", err)
	}
}

// TestSDKECRDescribeRepositoriesPagination guards maxResults/nextToken on
// DescribeRepositories.
func TestSDKECRDescribeRepositoriesPagination(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	for _, name := range []string{"r1", "r2", "r3"} {
		if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
			RepositoryName: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateRepository(%s): %v", name, err)
		}
	}

	first, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		MaxResults: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("DescribeRepositories(page1): %v", err)
	}

	if len(first.Repositories) != 2 || aws.ToString(first.NextToken) == "" {
		t.Fatalf("page1: got %d repos, nextToken=%q", len(first.Repositories), aws.ToString(first.NextToken))
	}

	second, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		MaxResults: aws.Int32(2),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeRepositories(page2): %v", err)
	}

	if len(second.Repositories) != 1 || aws.ToString(second.NextToken) != "" {
		t.Fatalf("page2: got %d repos, nextToken=%q", len(second.Repositories), aws.ToString(second.NextToken))
	}
}
