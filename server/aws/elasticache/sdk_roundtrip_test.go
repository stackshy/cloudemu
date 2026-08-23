package elasticache_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSDKClient(t *testing.T) *awselasticache.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		ElastiCache: cloud.ElastiCache,
		// EC2 also wired so we exercise the dispatch precedence: a request for
		// ElastiCache must claim the body before EC2 sees it.
		EC2: cloud.EC2,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awselasticache.NewFromConfig(cfg, func(o *awselasticache.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// TestSDKModifyCacheCluster is a regression guard for issue #319:
// ModifyCacheCluster was unimplemented (InvalidAction).
func TestSDKModifyCacheCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("mc"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	out, err := client.ModifyCacheCluster(ctx, &awselasticache.ModifyCacheClusterInput{
		CacheClusterId: aws.String("mc"), CacheNodeType: aws.String("cache.t3.medium"),
	})
	if err != nil {
		t.Fatalf("ModifyCacheCluster: %v", err)
	}

	if aws.ToString(out.CacheCluster.CacheNodeType) != "cache.t3.medium" {
		t.Fatalf("node type = %q, want cache.t3.medium", aws.ToString(out.CacheCluster.CacheNodeType))
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("mc"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	if aws.ToString(got.CacheClusters[0].CacheNodeType) != "cache.t3.medium" {
		t.Fatalf("persisted node type = %q", aws.ToString(got.CacheClusters[0].CacheNodeType))
	}
}

func TestSDKElastiCacheLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("session-cache"),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
		Tags: []ectypes.Tag{
			{Key: aws.String("env"), Value: aws.String("staging")},
		},
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	if aws.ToString(out.CacheCluster.CacheClusterId) != "session-cache" {
		t.Fatalf("got id %q, want session-cache", aws.ToString(out.CacheCluster.CacheClusterId))
	}

	if aws.ToString(out.CacheCluster.CacheClusterStatus) != "available" {
		t.Fatalf("got status %q, want available", aws.ToString(out.CacheCluster.CacheClusterStatus))
	}

	if aws.ToString(out.CacheCluster.Engine) != "redis" {
		t.Fatalf("got engine %q, want redis", aws.ToString(out.CacheCluster.Engine))
	}

	// Describe by id.
	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId:    aws.String("session-cache"),
		ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	if len(got.CacheClusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(got.CacheClusters))
	}

	cc := got.CacheClusters[0]
	if len(cc.CacheNodes) != 1 || cc.CacheNodes[0].Endpoint == nil ||
		aws.ToString(cc.CacheNodes[0].Endpoint.Address) == "" {
		t.Fatalf("expected a cache node with an endpoint, got %+v", cc.CacheNodes)
	}

	// List (no id): should include the one cluster.
	list, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{})
	if err != nil {
		t.Fatalf("DescribeCacheClusters(all): %v", err)
	}

	if len(list.CacheClusters) != 1 {
		t.Fatalf("list: got %d clusters, want 1", len(list.CacheClusters))
	}

	// Delete.
	del, err := client.DeleteCacheCluster(ctx, &awselasticache.DeleteCacheClusterInput{
		CacheClusterId: aws.String("session-cache"),
	})
	if err != nil {
		t.Fatalf("DeleteCacheCluster: %v", err)
	}

	if aws.ToString(del.CacheCluster.CacheClusterStatus) != "deleting" {
		t.Fatalf("delete status = %q, want deleting", aws.ToString(del.CacheCluster.CacheClusterStatus))
	}

	// Get after delete -> CacheClusterNotFound.
	_, err = client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("session-cache"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "CacheClusterNotFound" {
		t.Fatalf("Describe after delete: got %v, want CacheClusterNotFound", err)
	}
}

func TestSDKElastiCacheNotFound(t *testing.T) {
	client := newSDKClient(t)

	_, err := client.DescribeCacheClusters(context.Background(),
		&awselasticache.DescribeCacheClustersInput{
			CacheClusterId: aws.String("missing"),
		})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "CacheClusterNotFound" {
		t.Fatalf("Describe(missing): got %v, want CacheClusterNotFound", err)
	}
}

// Sanity check: when both ElastiCache and EC2 handlers are wired, an EC2
// request still reaches the EC2 handler — the ElastiCache handler's Matches
// must reject non-ElastiCache actions despite parsing the form first.
func TestSDKElastiCacheDoesNotShadowEC2(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		ElastiCache: cloud.ElastiCache,
		EC2:         cloud.EC2,
		VPC:         cloud.VPC,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ec2Client := awsec2.NewFromConfig(cfg, func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	out, err := ec2Client.RunInstances(context.Background(), &awsec2.RunInstancesInput{
		ImageId:  aws.String("ami-1"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("EC2 RunInstances through combined server: %v", err)
	}

	if len(out.Instances) == 0 {
		t.Fatal("expected at least one EC2 instance")
	}
}

// TestSDKRebootCacheCluster covers the RebootCacheCluster dispatch: a normal
// lifecycle must be able to reboot a cluster to apply parameter changes.
func TestSDKRebootCacheCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("rc"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	out, err := client.RebootCacheCluster(ctx, &awselasticache.RebootCacheClusterInput{
		CacheClusterId: aws.String("rc"), CacheNodeIdsToReboot: []string{"0001"},
	})
	if err != nil {
		t.Fatalf("RebootCacheCluster: %v", err)
	}

	if aws.ToString(out.CacheCluster.CacheClusterId) != "rc" {
		t.Fatalf("reboot returned wrong cluster: %+v", out.CacheCluster)
	}
}

// TestSDKCacheClusterFields guards the ARN, EngineVersion and CacheParameterGroup
// fields that Terraform reads from Describe.
func TestSDKCacheClusterFields(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("cf"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("cf"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	c := got.CacheClusters[0]
	if aws.ToString(c.ARN) == "" {
		t.Fatal("ARN empty")
	}

	if aws.ToString(c.EngineVersion) != "7.1" {
		t.Fatalf("EngineVersion = %q, want 7.1", aws.ToString(c.EngineVersion))
	}

	if c.CacheParameterGroup == nil ||
		aws.ToString(c.CacheParameterGroup.CacheParameterGroupName) != "default.redis7" {
		t.Fatalf("CacheParameterGroup = %+v", c.CacheParameterGroup)
	}
}

// TestSDKCacheClusterTagging covers Add/List/RemoveTagsFromResource by ARN.
func TestSDKCacheClusterTagging(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("tg"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		Tags: []ectypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	arn := aws.ToString(created.CacheCluster.ARN)

	listed, err := client.ListTagsForResource(ctx, &awselasticache.ListTagsForResourceInput{
		ResourceName: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(listed.TagList) != 1 || aws.ToString(listed.TagList[0].Key) != "env" {
		t.Fatalf("create-time tags not returned: %+v", listed.TagList)
	}

	if _, err := client.AddTagsToResource(ctx, &awselasticache.AddTagsToResourceInput{
		ResourceName: aws.String(arn),
		Tags:         []ectypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}},
	}); err != nil {
		t.Fatalf("AddTagsToResource: %v", err)
	}

	after, err := client.RemoveTagsFromResource(ctx, &awselasticache.RemoveTagsFromResourceInput{
		ResourceName: aws.String(arn), TagKeys: []string{"env"},
	})
	if err != nil {
		t.Fatalf("RemoveTagsFromResource: %v", err)
	}

	if len(after.TagList) != 1 || aws.ToString(after.TagList[0].Key) != "team" {
		t.Fatalf("after remove, tags = %+v", after.TagList)
	}
}

// TestSDKCacheParameterGroups covers Create/Describe/Delete of parameter groups
// and DescribeCacheEngineVersions.
func TestSDKCacheParameterGroups(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName:   aws.String("pg1"),
		CacheParameterGroupFamily: aws.String("redis7"),
		Description:               aws.String("custom"),
	}); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	got, err := client.DescribeCacheParameterGroups(ctx, &awselasticache.DescribeCacheParameterGroupsInput{
		CacheParameterGroupName: aws.String("pg1"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheParameterGroups: %v", err)
	}

	if len(got.CacheParameterGroups) != 1 ||
		aws.ToString(got.CacheParameterGroups[0].CacheParameterGroupFamily) != "redis7" {
		t.Fatalf("describe parameter groups = %+v", got.CacheParameterGroups)
	}

	if _, err := client.DeleteCacheParameterGroup(ctx, &awselasticache.DeleteCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg1"),
	}); err != nil {
		t.Fatalf("DeleteCacheParameterGroup: %v", err)
	}

	versions, err := client.DescribeCacheEngineVersions(ctx, &awselasticache.DescribeCacheEngineVersionsInput{
		Engine: aws.String("redis"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheEngineVersions: %v", err)
	}

	if len(versions.CacheEngineVersions) == 0 {
		t.Fatal("expected at least one redis engine version")
	}
}
