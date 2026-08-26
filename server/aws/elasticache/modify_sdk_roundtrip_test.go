package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// TestSDKModifyCacheClusterScaleAndVersion covers that ModifyCacheCluster
// applies a Memcached NumCacheNodes rescale and an EngineVersion bump, both
// reflected on Describe (Terraform convergence).
func TestSDKModifyCacheClusterScaleAndVersion(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("mm"), Engine: aws.String("memcached"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		EngineVersion: aws.String("1.6.17"),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	if _, err := client.ModifyCacheCluster(ctx, &awselasticache.ModifyCacheClusterInput{
		CacheClusterId: aws.String("mm"), NumCacheNodes: aws.Int32(3),
		EngineVersion: aws.String("1.6.22"), ApplyImmediately: aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("mm"), ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	c := got.CacheClusters[0]
	if aws.ToInt32(c.NumCacheNodes) != 3 {
		t.Fatalf("NumCacheNodes = %d, want 3", aws.ToInt32(c.NumCacheNodes))
	}

	if len(c.CacheNodes) != 3 {
		t.Fatalf("CacheNodes = %d, want 3", len(c.CacheNodes))
	}

	if aws.ToString(c.EngineVersion) != "1.6.22" {
		t.Fatalf("EngineVersion = %q, want 1.6.22", aws.ToString(c.EngineVersion))
	}
}
