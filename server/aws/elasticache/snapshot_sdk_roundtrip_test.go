package elasticache_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSnapshotClient(t *testing.T) *awselasticache.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awselasticache.NewFromConfig(cfg)
}

func mustCreateCluster(t *testing.T, c *awselasticache.Client, id string) {
	t.Helper()

	_, err := c.CreateCacheCluster(context.Background(), &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(id),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster(%s): %v", id, err)
	}
}

// TestCreateSnapshotMemcachedUnsupported guards that snapshotting a Memcached
// cluster is rejected — real ElastiCache snapshots are "valid for Valkey or
// Redis OSS only" and return SnapshotFeatureNotSupportedFault.
func TestCreateSnapshotMemcachedUnsupported(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	if _, err := c.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("mcs"),
		Engine:         aws.String("memcached"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	_, err := c.CreateSnapshot(ctx, &awselasticache.CreateSnapshotInput{
		SnapshotName:   aws.String("snap-mc"),
		CacheClusterId: aws.String("mcs"),
	})
	if err == nil {
		t.Fatal("snapshotting a Memcached cluster should fail")
	}

	var unsupported *ectypes.SnapshotFeatureNotSupportedFault
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %v, want *SnapshotFeatureNotSupportedFault", err)
	}
}

func TestCreateSnapshotSDKRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	mustCreateCluster(t, c, "cluster-1")

	out, err := c.CreateSnapshot(ctx, &awselasticache.CreateSnapshotInput{
		SnapshotName:   aws.String("snap-1"),
		CacheClusterId: aws.String("cluster-1"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	s := out.Snapshot
	if s == nil {
		t.Fatal("CreateSnapshot returned nil Snapshot")
	}

	if got := aws.ToString(s.SnapshotName); got != "snap-1" {
		t.Errorf("SnapshotName = %q, want snap-1", got)
	}
	if got := aws.ToString(s.CacheClusterId); got != "cluster-1" {
		t.Errorf("CacheClusterId = %q, want cluster-1", got)
	}
	if got := aws.ToString(s.SnapshotStatus); got != "available" {
		t.Errorf("SnapshotStatus = %q, want available", got)
	}
	if got := aws.ToString(s.SnapshotSource); got != "manual" {
		t.Errorf("SnapshotSource = %q, want manual", got)
	}
	if got := aws.ToString(s.Engine); got != "redis" {
		t.Errorf("Engine = %q, want redis", got)
	}
	if got := aws.ToString(s.CacheNodeType); got != "cache.t3.micro" {
		t.Errorf("CacheNodeType = %q, want cache.t3.micro", got)
	}
	if got := aws.ToInt32(s.NumCacheNodes); got != 1 {
		t.Errorf("NumCacheNodes = %d, want 1", got)
	}
	if got := aws.ToInt32(s.Port); got != 6379 {
		t.Errorf("Port = %d, want 6379", got)
	}
	if aws.ToString(s.ARN) == "" {
		t.Error("ARN is empty")
	}
}

func TestDescribeSnapshotsSDKRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	mustCreateCluster(t, c, "cluster-a")
	mustCreateCluster(t, c, "cluster-b")

	for _, in := range []awselasticache.CreateSnapshotInput{
		{SnapshotName: aws.String("snap-a"), CacheClusterId: aws.String("cluster-a")},
		{SnapshotName: aws.String("snap-b"), CacheClusterId: aws.String("cluster-b")},
	} {
		if _, err := c.CreateSnapshot(ctx, &in); err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", aws.ToString(in.SnapshotName), err)
		}
	}

	all, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}
	if len(all.Snapshots) != 2 {
		t.Fatalf("DescribeSnapshots returned %d, want 2", len(all.Snapshots))
	}

	byName, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
		SnapshotName: aws.String("snap-a"),
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots by name: %v", err)
	}
	if len(byName.Snapshots) != 1 {
		t.Fatalf("filter by name returned %d, want 1", len(byName.Snapshots))
	}
	if got := aws.ToString(byName.Snapshots[0].SnapshotName); got != "snap-a" {
		t.Errorf("filtered name = %q, want snap-a", got)
	}

	byCluster, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
		CacheClusterId: aws.String("cluster-b"),
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots by cluster: %v", err)
	}
	if len(byCluster.Snapshots) != 1 {
		t.Fatalf("filter by cluster returned %d, want 1", len(byCluster.Snapshots))
	}
	if got := aws.ToString(byCluster.Snapshots[0].CacheClusterId); got != "cluster-b" {
		t.Errorf("filtered cluster = %q, want cluster-b", got)
	}
}

func TestCreateSnapshotDuplicateReturnsSnapshotAlreadyExistsFault(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	mustCreateCluster(t, c, "cluster-dup")

	in := &awselasticache.CreateSnapshotInput{
		SnapshotName:   aws.String("snap-dup"),
		CacheClusterId: aws.String("cluster-dup"),
	}
	if _, err := c.CreateSnapshot(ctx, in); err != nil {
		t.Fatalf("first CreateSnapshot: %v", err)
	}

	_, err := c.CreateSnapshot(ctx, in)
	if err == nil {
		t.Fatal("duplicate CreateSnapshot should fail")
	}

	var already *ectypes.SnapshotAlreadyExistsFault
	if !errors.As(err, &already) {
		t.Fatalf("error = %v, want SnapshotAlreadyExistsFault", err)
	}
}

func TestCreateSnapshotMissingClusterReturnsCacheClusterNotFound(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	_, err := c.CreateSnapshot(ctx, &awselasticache.CreateSnapshotInput{
		SnapshotName:   aws.String("snap-x"),
		CacheClusterId: aws.String("no-such-cluster"),
	})
	if err == nil {
		t.Fatal("CreateSnapshot for a missing cluster should fail")
	}

	var notFound *ectypes.CacheClusterNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want CacheClusterNotFoundFault", err)
	}
}

func TestDescribeSnapshotsUnknownNameReturnsSnapshotNotFoundFault(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	_, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
		SnapshotName: aws.String("ghost"),
	})
	if err == nil {
		t.Fatal("DescribeSnapshots for an unknown name should fail")
	}

	var notFound *ectypes.SnapshotNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want SnapshotNotFoundFault", err)
	}
}
