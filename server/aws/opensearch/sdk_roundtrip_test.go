package opensearch_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsos "github.com/aws/aws-sdk-go-v2/service/opensearch"
	ostypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newOSClient(t *testing.T) *awsos.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{OpenSearch: cloud.OpenSearch})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsos.NewFromConfig(cfg, func(o *awsos.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKDomainLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	create, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{
		DomainName:    aws.String("sdk-domain"),
		EngineVersion: aws.String("OpenSearch_2.11"),
		ClusterConfig: &ostypes.ClusterConfig{
			InstanceType:  ostypes.OpenSearchPartitionInstanceTypeT3SmallSearch,
			InstanceCount: aws.Int32(2),
		},
		TagList: []ostypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	arn := aws.ToString(create.DomainStatus.ARN)
	if !strings.Contains(arn, ":es:") {
		t.Fatalf("unexpected ARN: %s", arn)
	}

	desc, err := c.DescribeDomain(ctx, &awsos.DescribeDomainInput{DomainName: aws.String("sdk-domain")})
	if err != nil {
		t.Fatalf("DescribeDomain: %v", err)
	}

	if aws.ToInt32(desc.DomainStatus.ClusterConfig.InstanceCount) != 2 {
		t.Fatalf("instance count = %d, want 2", aws.ToInt32(desc.DomainStatus.ClusterConfig.InstanceCount))
	}

	if aws.ToString(desc.DomainStatus.Endpoint) == "" {
		t.Fatalf("expected non-empty endpoint")
	}

	// UpdateDomainConfig.
	upd, err := c.UpdateDomainConfig(ctx, &awsos.UpdateDomainConfigInput{
		DomainName: aws.String("sdk-domain"),
		ClusterConfig: &ostypes.ClusterConfig{
			InstanceType:  ostypes.OpenSearchPartitionInstanceTypeM6gLargeSearch,
			InstanceCount: aws.Int32(4),
		},
	})
	if err != nil {
		t.Fatalf("UpdateDomainConfig: %v", err)
	}

	if aws.ToInt32(upd.DomainConfig.ClusterConfig.Options.InstanceCount) != 4 {
		t.Fatalf("updated count = %d, want 4", aws.ToInt32(upd.DomainConfig.ClusterConfig.Options.InstanceCount))
	}

	// ListDomainNames.
	names, err := c.ListDomainNames(ctx, &awsos.ListDomainNamesInput{})
	if err != nil {
		t.Fatalf("ListDomainNames: %v", err)
	}

	if len(names.DomainNames) != 1 || aws.ToString(names.DomainNames[0].DomainName) != "sdk-domain" {
		t.Fatalf("unexpected domain names: %+v", names.DomainNames)
	}

	// DeleteDomain.
	if _, err := c.DeleteDomain(ctx, &awsos.DeleteDomainInput{DomainName: aws.String("sdk-domain")}); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}

	names, _ = c.ListDomainNames(ctx, &awsos.ListDomainNamesInput{})
	if len(names.DomainNames) != 0 {
		t.Fatalf("expected no domains after delete, got %+v", names.DomainNames)
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	create, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{DomainName: aws.String("tag-domain")})
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	arn := create.DomainStatus.ARN

	if _, err := c.AddTags(ctx, &awsos.AddTagsInput{
		ARN:     arn,
		TagList: []ostypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
	}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}

	tags, err := c.ListTags(ctx, &awsos.ListTagsInput{ARN: arn})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if len(tags.TagList) != 1 || aws.ToString(tags.TagList[0].Key) != "team" {
		t.Fatalf("unexpected tags: %+v", tags.TagList)
	}

	if _, err := c.RemoveTags(ctx, &awsos.RemoveTagsInput{ARN: arn, TagKeys: []string{"team"}}); err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}

	tags, _ = c.ListTags(ctx, &awsos.ListTagsInput{ARN: arn})
	if len(tags.TagList) != 0 {
		t.Fatalf("expected no tags after remove, got %+v", tags.TagList)
	}
}

func TestSDKDescribeMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	_, err := c.DescribeDomain(ctx, &awsos.DescribeDomainInput{DomainName: aws.String("missing-domain")})
	if err == nil {
		t.Fatal("expected error for missing domain")
	}

	var nf *ostypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ResourceNotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestSDKCreateDuplicateReturnsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	if _, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{DomainName: aws.String("dup-domain")}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{DomainName: aws.String("dup-domain")})
	if err == nil {
		t.Fatal("expected duplicate error")
	}

	var exists *ostypes.ResourceAlreadyExistsException
	if !errors.As(err, &exists) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ResourceAlreadyExistsException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ResourceAlreadyExistsException, got %v", err)
	}
}

func TestSDKCreateInvalidNameReturnsValidation(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	_, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{DomainName: aws.String("A")})
	if err == nil {
		t.Fatal("expected validation error")
	}

	var ve *ostypes.ValidationException
	if !errors.As(err, &ve) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ValidationException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ValidationException, got %v", err)
	}
}

func TestSDKVersionsAndPackages(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	versions, err := c.ListVersions(ctx, &awsos.ListVersionsInput{})
	if err != nil || len(versions.Versions) == 0 {
		t.Fatalf("ListVersions: %v len=%d", err, len(versions.Versions))
	}

	pkg, err := c.CreatePackage(ctx, &awsos.CreatePackageInput{
		PackageName: aws.String("my-dict"),
		PackageType: ostypes.PackageTypeTxtDictionary,
		PackageSource: &ostypes.PackageSource{
			S3BucketName: aws.String("bucket"),
			S3Key:        aws.String("dict.txt"),
		},
	})
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}

	if aws.ToString(pkg.PackageDetails.PackageID) == "" {
		t.Fatalf("expected package id")
	}

	listed, err := c.DescribePackages(ctx, &awsos.DescribePackagesInput{})
	if err != nil || len(listed.PackageDetailsList) != 1 {
		t.Fatalf("DescribePackages: %v len=%d", err, len(listed.PackageDetailsList))
	}
}
