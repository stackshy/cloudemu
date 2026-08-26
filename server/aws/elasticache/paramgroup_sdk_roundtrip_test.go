package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// TestSDKCustomParameterGroupPersisted covers that a submitted
// CacheParameterGroupName is echoed back on Describe (IaC convergence), while an
// omitted one still reports the engine family's default.
func TestSDKCustomParameterGroupPersisted(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName:   aws.String("myredis"),
		CacheParameterGroupFamily: aws.String("redis7"),
		Description:               aws.String("custom"),
	}); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("c1"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		CacheParameterGroupName: aws.String("myredis"),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("c1"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	pg := got.CacheClusters[0].CacheParameterGroup
	if pg == nil || aws.ToString(pg.CacheParameterGroupName) != "myredis" {
		t.Fatalf("CacheParameterGroup = %+v, want myredis", pg)
	}
}

// TestSDKDefaultParameterGroupWhenOmitted covers that omitting
// CacheParameterGroupName keeps the derived default.<family> value.
func TestSDKDefaultParameterGroupWhenOmitted(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("c2"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("c2"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	pg := got.CacheClusters[0].CacheParameterGroup
	if pg == nil || aws.ToString(pg.CacheParameterGroupName) != "default.redis7" {
		t.Fatalf("CacheParameterGroup = %+v, want default.redis7", pg)
	}
}
