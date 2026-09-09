package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// TestSDKReplicationGroupMembersDescribable guards the Terraform-breaking bug:
// after CreateReplicationGroup, terraform-provider-aws reads every member cache
// cluster back by id (DescribeCacheClusters). A member that returns
// CacheClusterNotFound aborts `terraform apply`. Each member must be describable
// as a single-node cache cluster carrying the ReplicationGroupId back-reference
// and a well-formed node (with a non-nil CustomerAvailabilityZone).
func TestSDKReplicationGroupMembersDescribable(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("rg"),
		ReplicationGroupDescription: aws.String("t"),
		Engine:                      aws.String("redis"),
		CacheNodeType:               aws.String("cache.t3.micro"),
		NumCacheClusters:            aws.Int32(2),
	}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	for _, id := range []string{"rg-001", "rg-002"} {
		out, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
			CacheClusterId: aws.String(id), ShowCacheNodeInfo: aws.Bool(true),
		})
		if err != nil {
			t.Fatalf("DescribeCacheClusters(%s): %v", id, err)
		}

		cc := out.CacheClusters[0]
		if aws.ToString(cc.ReplicationGroupId) != "rg" {
			t.Fatalf("member %s ReplicationGroupId = %q, want rg", id, aws.ToString(cc.ReplicationGroupId))
		}

		if aws.ToString(cc.Engine) != "redis" {
			t.Fatalf("member %s Engine = %q, want redis", id, aws.ToString(cc.Engine))
		}

		if len(cc.CacheNodes) != 1 || aws.ToString(cc.CacheNodes[0].CustomerAvailabilityZone) == "" {
			t.Fatalf("member %s missing a well-formed CacheNode", id)
		}
	}
}
