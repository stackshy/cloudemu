package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: DescribeCacheClusters must return the same sequence on every call.
// The pre-fix bug was random map iteration order, so repetition is the signal.
func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	names := []string{"zeta", "alpha", "mid", "beta", "omega"}
	for _, name := range names {
		if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
			CacheClusterId: aws.String(name),
			Engine:         aws.String("redis"),
			CacheNodeType:  aws.String("cache.t3.micro"),
			NumCacheNodes:  aws.Int32(1),
		}); err != nil {
			t.Fatalf("CreateCacheCluster(%s): %v", name, err)
		}
	}

	list := func() []string {
		out, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{})
		if err != nil {
			t.Fatalf("DescribeCacheClusters: %v", err)
		}

		got := make([]string, 0, len(out.CacheClusters))
		for i := range out.CacheClusters {
			got = append(got, aws.ToString(out.CacheClusters[i].CacheClusterId))
		}

		return got
	}

	first := list()
	if len(first) != len(names) {
		t.Fatalf("DescribeCacheClusters returned %d clusters, want %d: %v", len(first), len(names), first)
	}

	for call := 2; call <= 5; call++ {
		got := list()
		if len(got) != len(first) {
			t.Fatalf("call %d: got %d clusters, want %d", call, len(got), len(first))
		}

		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("call %d: order diverged at index %d: got %v, first call %v", call, i, got, first)
			}
		}
	}
}
