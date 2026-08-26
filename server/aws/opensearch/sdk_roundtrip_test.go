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

func TestSDKDescribeDomainConfigEnvelope(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	if _, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{
		DomainName:    aws.String("cfg-domain"),
		EngineVersion: aws.String("OpenSearch_2.11"),
		EBSOptions: &ostypes.EBSOptions{
			EBSEnabled: aws.Bool(true),
			VolumeType: ostypes.VolumeTypeGp3,
			VolumeSize: aws.Int32(20),
		},
		VPCOptions: &ostypes.VPCOptions{
			SubnetIds: []string{"subnet-aaa", "subnet-bbb"},
		},
		EncryptionAtRestOptions: &ostypes.EncryptionAtRestOptions{
			Enabled: aws.Bool(true),
		},
	}); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	cfg, err := c.DescribeDomainConfig(ctx, &awsos.DescribeDomainConfigInput{DomainName: aws.String("cfg-domain")})
	if err != nil {
		t.Fatalf("DescribeDomainConfig: %v", err)
	}

	dc := cfg.DomainConfig

	if dc.EBSOptions == nil || dc.EBSOptions.Options == nil {
		t.Fatalf("EBSOptions envelope not populated: %+v", dc.EBSOptions)
	}

	if got := aws.ToInt32(dc.EBSOptions.Options.VolumeSize); got != 20 {
		t.Fatalf("EBSOptions.Options.VolumeSize = %d, want 20", got)
	}

	if dc.EBSOptions.Status == nil || dc.EBSOptions.Status.State != ostypes.OptionStateActive {
		t.Fatalf("EBSOptions.Status.State not Active: %+v", dc.EBSOptions.Status)
	}

	if dc.VPCOptions == nil || dc.VPCOptions.Options == nil {
		t.Fatalf("VPCOptions envelope not populated: %+v", dc.VPCOptions)
	}

	if dc.EncryptionAtRestOptions == nil || dc.EncryptionAtRestOptions.Options == nil ||
		!aws.ToBool(dc.EncryptionAtRestOptions.Options.Enabled) {
		t.Fatalf("EncryptionAtRestOptions envelope not populated: %+v", dc.EncryptionAtRestOptions)
	}

	// Modeled blocks stay wrapped too.
	if dc.ClusterConfig == nil || dc.ClusterConfig.Options == nil || dc.ClusterConfig.Status == nil {
		t.Fatalf("ClusterConfig envelope regressed: %+v", dc.ClusterConfig)
	}

	// UpdateDomainConfig response returns the changed block in the envelope.
	upd, err := c.UpdateDomainConfig(ctx, &awsos.UpdateDomainConfigInput{
		DomainName: aws.String("cfg-domain"),
		EBSOptions: &ostypes.EBSOptions{
			EBSEnabled: aws.Bool(true),
			VolumeType: ostypes.VolumeTypeGp3,
			VolumeSize: aws.Int32(50),
		},
	})
	if err != nil {
		t.Fatalf("UpdateDomainConfig: %v", err)
	}

	if upd.DomainConfig.EBSOptions == nil || upd.DomainConfig.EBSOptions.Options == nil ||
		aws.ToInt32(upd.DomainConfig.EBSOptions.Options.VolumeSize) != 50 {
		t.Fatalf("UpdateDomainConfig EBS envelope = %+v, want VolumeSize=50", upd.DomainConfig.EBSOptions)
	}
}

func TestSDKVPCDomainEndpoints(t *testing.T) {
	ctx := context.Background()
	c := newOSClient(t)

	// VPC-access domain: no public Endpoint, Endpoints["vpc"] set, VPCId derived.
	vpc, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{
		DomainName: aws.String("vpc-domain"),
		VPCOptions: &ostypes.VPCOptions{SubnetIds: []string{"subnet-123"}},
	})
	if err != nil {
		t.Fatalf("CreateDomain (vpc): %v", err)
	}

	if aws.ToString(vpc.DomainStatus.Endpoint) != "" {
		t.Fatalf("VPC domain should have empty Endpoint, got %q", aws.ToString(vpc.DomainStatus.Endpoint))
	}

	if vpc.DomainStatus.Endpoints["vpc"] == "" {
		t.Fatalf("VPC domain should expose Endpoints[vpc], got %+v", vpc.DomainStatus.Endpoints)
	}

	desc, err := c.DescribeDomain(ctx, &awsos.DescribeDomainInput{DomainName: aws.String("vpc-domain")})
	if err != nil {
		t.Fatalf("DescribeDomain (vpc): %v", err)
	}

	if desc.DomainStatus.VPCOptions == nil || aws.ToString(desc.DomainStatus.VPCOptions.VPCId) == "" {
		t.Fatalf("VPCOptions.VPCId not enriched: %+v", desc.DomainStatus.VPCOptions)
	}

	// Public domain: keeps its single Endpoint, no vpc key.
	pub, err := c.CreateDomain(ctx, &awsos.CreateDomainInput{DomainName: aws.String("pub-domain")})
	if err != nil {
		t.Fatalf("CreateDomain (public): %v", err)
	}

	if aws.ToString(pub.DomainStatus.Endpoint) == "" {
		t.Fatalf("public domain should have an Endpoint")
	}

	if _, ok := pub.DomainStatus.Endpoints["vpc"]; ok {
		t.Fatalf("public domain should not expose Endpoints[vpc], got %+v", pub.DomainStatus.Endpoints)
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
