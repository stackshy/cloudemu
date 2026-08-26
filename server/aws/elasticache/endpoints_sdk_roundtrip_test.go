package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// TestSDKCacheClusterCustomPort covers that a submitted Port is honored on the
// per-node endpoint, and that an omitted Port defaults to the Redis port.
func TestSDKCacheClusterCustomPort(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("cp"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		Port: aws.Int32(6380),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("cp"), ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	nodes := got.CacheClusters[0].CacheNodes
	if len(nodes) != 1 || nodes[0].Endpoint == nil {
		t.Fatalf("CacheNodes = %+v", nodes)
	}

	if p := aws.ToInt32(nodes[0].Endpoint.Port); p != 6380 {
		t.Fatalf("custom port = %d, want 6380", p)
	}
}

// TestSDKCacheClusterDefaultRedisPort covers that an omitted Port on a Redis
// cluster defaults to 6379.
func TestSDKCacheClusterDefaultRedisPort(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("dp"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("dp"), ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	nodes := got.CacheClusters[0].CacheNodes
	if len(nodes) != 1 || nodes[0].Endpoint == nil {
		t.Fatalf("CacheNodes = %+v", nodes)
	}

	if p := aws.ToInt32(nodes[0].Endpoint.Port); p != 6379 {
		t.Fatalf("default redis port = %d, want 6379", p)
	}
}

// TestSDKRedisNoConfigurationEndpoint covers that a Redis cluster returns no
// ConfigurationEndpoint (a Memcached-only field in the real API); its address
// lives on the per-node endpoint instead.
func TestSDKRedisNoConfigurationEndpoint(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("rc"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("rc"), ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	c := got.CacheClusters[0]
	if c.ConfigurationEndpoint != nil {
		t.Fatalf("Redis ConfigurationEndpoint = %+v, want nil", c.ConfigurationEndpoint)
	}

	if len(c.CacheNodes) != 1 || c.CacheNodes[0].Endpoint == nil {
		t.Fatalf("Redis per-node endpoint missing: %+v", c.CacheNodes)
	}
}

// TestSDKMemcachedEndpoints covers that a Memcached cluster returns a
// ConfigurationEndpoint on port 11211 (host carrying ".cfg") and per-node
// endpoints on 11211, both when the port is omitted (defaulted per engine).
func TestSDKMemcachedEndpoints(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("mc"), Engine: aws.String("memcached"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(2),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("mc"), ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	c := got.CacheClusters[0]
	if c.ConfigurationEndpoint == nil {
		t.Fatal("Memcached ConfigurationEndpoint = nil, want non-nil")
	}

	if p := aws.ToInt32(c.ConfigurationEndpoint.Port); p != 11211 {
		t.Fatalf("Memcached ConfigurationEndpoint port = %d, want 11211", p)
	}

	if len(c.CacheNodes) != 2 {
		t.Fatalf("Memcached CacheNodes = %d, want 2", len(c.CacheNodes))
	}

	for i := range c.CacheNodes {
		if c.CacheNodes[i].Endpoint == nil || aws.ToInt32(c.CacheNodes[i].Endpoint.Port) != 11211 {
			t.Fatalf("Memcached node %d endpoint = %+v, want port 11211", i, c.CacheNodes[i].Endpoint)
		}
	}
}
