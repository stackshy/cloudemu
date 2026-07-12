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
