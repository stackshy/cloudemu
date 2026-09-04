package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// TestSDKCacheNodeCarriesAvailabilityZone guards the Terraform-breaking bug: the
// AWS provider dereferences CacheNode.CustomerAvailabilityZone unconditionally
// when flattening a cluster's nodes and aborts `terraform apply` with
// "Unexpected nil pointer" if it is absent. Every emitted node must carry a
// non-empty AZ. AutoMinorVersionUpgrade must also be reported (default true) so
// the provider does not see a perpetual diff against its own schema default.
func TestSDKCacheNodeCarriesAvailabilityZone(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("mc"), Engine: aws.String("memcached"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	if !aws.ToBool(created.CacheCluster.AutoMinorVersionUpgrade) {
		t.Fatalf("AutoMinorVersionUpgrade default = false, want true")
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("mc"), ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	nodes := got.CacheClusters[0].CacheNodes
	if len(nodes) != 2 {
		t.Fatalf("CacheNodes = %d, want 2", len(nodes))
	}

	for _, n := range nodes {
		if aws.ToString(n.CustomerAvailabilityZone) == "" {
			t.Fatalf("CacheNode %s has empty CustomerAvailabilityZone", aws.ToString(n.CacheNodeId))
		}
	}
}

// TestSDKAutoMinorVersionUpgradeRoundTrip guards that an explicit false is
// preserved rather than coerced back to the default true.
func TestSDKAutoMinorVersionUpgradeRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("off"), Engine: aws.String("redis"),
		NumCacheNodes: aws.Int32(1), AutoMinorVersionUpgrade: aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	if aws.ToBool(created.CacheCluster.AutoMinorVersionUpgrade) {
		t.Fatalf("AutoMinorVersionUpgrade = true, want the explicit false to round-trip")
	}

	got, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("off"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	if aws.ToBool(got.CacheClusters[0].AutoMinorVersionUpgrade) {
		t.Fatalf("persisted AutoMinorVersionUpgrade = true, want false")
	}
}
