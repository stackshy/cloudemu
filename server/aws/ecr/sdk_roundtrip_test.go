package ecr_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

const sampleManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`

func newECRClient(t *testing.T) *awsecr.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{ECR: cloud.ECR})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsecr.NewFromConfig(cfg, func(o *awsecr.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKECRRepositoryLifecycle(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	created, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName:             aws.String("app"),
		ImageTagMutability:         ecrtypes.ImageTagMutabilityImmutable,
		ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{ScanOnPush: true},
		Tags:                       []ecrtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if aws.ToString(created.Repository.RepositoryName) != "app" {
		t.Fatalf("got repo name %q, want app", aws.ToString(created.Repository.RepositoryName))
	}

	all, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{})
	if err != nil {
		t.Fatalf("DescribeRepositories(all): %v", err)
	}

	if len(all.Repositories) != 1 {
		t.Fatalf("got %d repositories, want 1", len(all.Repositories))
	}

	byName, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"app"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories(app): %v", err)
	}

	if len(byName.Repositories) != 1 || aws.ToString(byName.Repositories[0].RepositoryName) != "app" {
		t.Fatalf("DescribeRepositories(app) returned %+v", byName.Repositories)
	}

	if _, err := client.DeleteRepository(ctx, &awsecr.DeleteRepositoryInput{
		RepositoryName: aws.String("app"), Force: true,
	}); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}

	_, err = client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{RepositoryNames: []string{"app"}})

	var notFound *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("describe after delete: want RepositoryNotFoundException, got %v", err)
	}
}

func TestSDKECRImageLifecycle(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("imgs"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	put, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("imgs"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	})
	if err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	if aws.ToString(put.Image.ImageId.ImageTag) != "v1" || aws.ToString(put.Image.ImageId.ImageDigest) == "" {
		t.Fatalf("PutImage returned %+v", put.Image.ImageId)
	}

	listed, err := client.ListImages(ctx, &awsecr.ListImagesInput{RepositoryName: aws.String("imgs")})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}

	if len(listed.ImageIds) != 1 || aws.ToString(listed.ImageIds[0].ImageTag) != "v1" {
		t.Fatalf("ListImages returned %+v", listed.ImageIds)
	}

	described, err := client.DescribeImages(ctx, &awsecr.DescribeImagesInput{RepositoryName: aws.String("imgs")})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}

	if len(described.ImageDetails) != 1 || !contains(described.ImageDetails[0].ImageTags, "v1") {
		t.Fatalf("DescribeImages returned %+v", described.ImageDetails)
	}

	deleted, err := client.BatchDeleteImage(ctx, &awsecr.BatchDeleteImageInput{
		RepositoryName: aws.String("imgs"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("v1")}},
	})
	if err != nil {
		t.Fatalf("BatchDeleteImage: %v", err)
	}

	if len(deleted.ImageIds) != 1 || len(deleted.Failures) != 0 {
		t.Fatalf("BatchDeleteImage returned ids=%+v failures=%+v", deleted.ImageIds, deleted.Failures)
	}
}

func TestSDKECRBatchDeleteMissingImage(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("partial"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	// Deleting an image that was never pushed yields a per-image failure, not a
	// thrown error.
	out, err := client.BatchDeleteImage(ctx, &awsecr.BatchDeleteImageInput{
		RepositoryName: aws.String("partial"),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String("ghost")}},
	})
	if err != nil {
		t.Fatalf("BatchDeleteImage: %v", err)
	}

	if len(out.Failures) != 1 || out.Failures[0].FailureCode != ecrtypes.ImageFailureCodeImageNotFound {
		t.Fatalf("want one ImageNotFound failure, got %+v", out.Failures)
	}
}

func TestSDKECRErrors(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("dup"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{RepositoryName: aws.String("dup")})

	var exists *ecrtypes.RepositoryAlreadyExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate create: want RepositoryAlreadyExistsException, got %v", err)
	}

	_, err = client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("ghost"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	})

	var notFound *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("put image on missing repo: want RepositoryNotFoundException, got %v", err)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}

	return false
}
