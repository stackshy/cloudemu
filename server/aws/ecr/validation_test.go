package ecr_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/smithy-go"
)

func requireECRCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

func TestSDKCreateRepositoryInvalidName(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	_, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{RepositoryName: aws.String("Invalid_Repo_UPPER")})
	requireECRCode(t, err, "InvalidParameterException")

	if _, err = client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{RepositoryName: aws.String("team/app")}); err != nil {
		t.Fatalf("valid name: %v", err)
	}
}

func TestSDKPutImageDigestMismatch(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{RepositoryName: aws.String("r1")}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("r1"), ImageManifest: aws.String("{}"), ImageTag: aws.String("v1"),
		ImageDigest: aws.String("sha256:" + strings.Repeat("0", 64)),
	})
	requireECRCode(t, err, "ImageDigestDoesNotMatchException")

	if _, err = client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("r1"), ImageManifest: aws.String("{}"), ImageTag: aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage without digest: %v", err)
	}
}
