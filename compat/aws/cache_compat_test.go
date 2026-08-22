package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSCacheCompat drives the ElastiCache cache-cluster control plane through
// the real aws-sdk-go-v2 client. Operation names match the portable "cache"
// driver in docs/coverage/coverage.json: CreateCacheCluster maps to
// "CreateCache", DescribeCacheClusters maps to "GetCache" (by id) and
// "ListCaches" (no id), ModifyCacheCluster maps to "UpdateCache", and
// DeleteCacheCluster maps to "DeleteCache".
//
// The Redis data-plane ops (Get/Set/Incr/Expire/Keys/... ) are not part of the
// AWS ElastiCache query protocol, so the wire handler does not route them —
// they are reported as gaps, not asserted here.
func TestAWSCacheCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{ElastiCache: provider.ElastiCache})

	client := awselasticache.NewFromConfig(sess.Config(), func(o *awselasticache.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc      = "cache"
		id       = "session-cache"
		engine   = "redis"
		nodeType = "cache.t3.micro"
		bigger   = "cache.t3.medium"
		numNodes = 1
	)

	sess.Op(svc, "CreateCache", func() error {
		out, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
			CacheClusterId: aws.String(id),
			Engine:         aws.String(engine),
			CacheNodeType:  aws.String(nodeType),
			NumCacheNodes:  aws.Int32(numNodes),
			Tags:           []ectypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
		})
		if err != nil {
			return err
		}

		if aws.ToString(out.CacheCluster.CacheClusterId) != id {
			return fmt.Errorf("CreateCacheCluster id = %q, want %q", aws.ToString(out.CacheCluster.CacheClusterId), id)
		}

		return nil
	})

	sess.Op(svc, "GetCache", func() error {
		out, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
			CacheClusterId: aws.String(id),
		})
		if err != nil {
			return err
		}

		if len(out.CacheClusters) != 1 {
			return fmt.Errorf("DescribeCacheClusters(id) = %d clusters, want 1", len(out.CacheClusters))
		}

		return nil
	})

	sess.Op(svc, "ListCaches", func() error {
		out, err := client.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{})
		if err != nil {
			return err
		}

		for _, cc := range out.CacheClusters {
			if aws.ToString(cc.CacheClusterId) == id {
				return nil
			}
		}

		return fmt.Errorf("cluster %q not found in DescribeCacheClusters", id)
	})

	sess.Op(svc, "UpdateCache", func() error {
		out, err := client.ModifyCacheCluster(ctx, &awselasticache.ModifyCacheClusterInput{
			CacheClusterId: aws.String(id),
			CacheNodeType:  aws.String(bigger),
		})
		if err != nil {
			return err
		}

		if aws.ToString(out.CacheCluster.CacheNodeType) != bigger {
			return fmt.Errorf("ModifyCacheCluster node type = %q, want %q", aws.ToString(out.CacheCluster.CacheNodeType), bigger)
		}

		return nil
	})

	sess.Op(svc, "DeleteCache", func() error {
		_, err := client.DeleteCacheCluster(ctx, &awselasticache.DeleteCacheClusterInput{
			CacheClusterId: aws.String(id),
		})

		return err
	})
}
