package ecr_test

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/stackshy/cloudemu/v2"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// TestSDKECRBatchGetImageRecordsPull pins that an image pull over the wire
// (BatchGetImage, the manifest fetch of `docker pull`) publishes the real
// AWS/ECR RepositoryPullCount metric, and that a push publishes nothing. Real
// ECR has no push-count metric.
func TestSDKECRBatchGetImageRecordsPull(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{ECR: cloud.ECR}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awsecr.NewFromConfig(cfg, func(o *awsecr.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	if _, err = client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{RepositoryName: aws.String("pulls")}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err = client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("pulls"), ImageManifest: aws.String(sampleManifest), ImageTag: aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	names, err := cloud.CloudWatch.ListMetrics(ctx, "AWS/ECR")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}

	if len(names) != 0 {
		t.Fatalf("push must publish no AWS/ECR metric, got %v", names)
	}

	if _, err = client.BatchGetImage(ctx, &awsecr.BatchGetImageInput{
		RepositoryName: aws.String("pulls"), ImageIds: []ecrtypes.ImageIdentifier{{ImageTag: aws.String("v1")}},
	}); err != nil {
		t.Fatalf("BatchGetImage: %v", err)
	}

	names, err = cloud.CloudWatch.ListMetrics(ctx, "AWS/ECR")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}

	if !slices.Equal(names, []string{"RepositoryPullCount"}) {
		t.Fatalf("AWS/ECR metrics after pull = %v, want [RepositoryPullCount]", names)
	}
}

// vanishingRegistry wraps a real registry but reports every image as gone at
// GetImage time. This models the race where an image is deleted between BatchGetImage's
// listing and its per-image fetch.
type vanishingRegistry struct {
	crdriver.ContainerRegistry
}

func (vanishingRegistry) GetImage(context.Context, string, string) (*crdriver.ImageDetail, error) {
	return nil, cerrors.New(cerrors.NotFound, "image vanished")
}

// TestSDKECRBatchGetImageVanishedImageIsFailure pins that an image deleted
// mid-request becomes a per-image ImageNotFound failure entry (the request
// itself still succeeds) rather than failing the whole BatchGetImage.
func TestSDKECRBatchGetImageVanishedImageIsFailure(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{ECR: vanishingRegistry{cloud.ECR}}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awsecr.NewFromConfig(cfg, func(o *awsecr.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	if _, err = client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{RepositoryName: aws.String("gone")}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err = client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("gone"), ImageManifest: aws.String(sampleManifest), ImageTag: aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	out, err := client.BatchGetImage(ctx, &awsecr.BatchGetImageInput{
		RepositoryName: aws.String("gone"), ImageIds: []ecrtypes.ImageIdentifier{{ImageTag: aws.String("v1")}},
	})
	if err != nil {
		t.Fatalf("BatchGetImage must succeed with a failure entry, got error: %v", err)
	}

	if len(out.Images) != 0 || len(out.Failures) != 1 || out.Failures[0].FailureCode != ecrtypes.ImageFailureCodeImageNotFound {
		t.Fatalf("images=%d failures=%+v, want 0 images and one ImageNotFound", len(out.Images), out.Failures)
	}
}
