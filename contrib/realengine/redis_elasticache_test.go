package realengine_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/redis/go-redis/v9"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestElastiCacheRedisE2E runs the real-user flow: create an ElastiCache Redis
// cluster with the AWS SDK, read the node endpoint, connect to it with a real
// Redis client, run real commands, then delete the cluster — all against
// CloudEmu backed by a real in-process Redis (no Docker, no cloud account).
func TestElastiCacheRedisE2E(t *testing.T) {
	eng := realengine.NewRedis()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAWS(config.WithCacheEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := elasticache.NewFromConfig(cfg, func(o *elasticache.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const clusterID = "app-cache"

	// 1. Create the cluster — like `aws elasticache create-cache-cluster`.
	if _, err := client.CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(clusterID),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	// 2. Read the node endpoint the SDK reports — the real Redis address.
	desc, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId:    aws.String(clusterID),
		ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	if len(desc.CacheClusters) != 1 || len(desc.CacheClusters[0].CacheNodes) != 1 {
		t.Fatalf("expected 1 cluster with 1 node, got %+v", desc.CacheClusters)
	}

	ep := desc.CacheClusters[0].CacheNodes[0].Endpoint
	if ep == nil {
		t.Fatal("no node endpoint reported")
	}

	addr := fmt.Sprintf("%s:%d", aws.ToString(ep.Address), aws.ToInt32(ep.Port))

	// 3. Connect with a real Redis client and run real commands.
	rdb := redis.NewClient(&redis.Options{Addr: addr})

	if err := rdb.Set(ctx, "session:42", "active", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}

	got, err := rdb.Get(ctx, "session:42").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	if got != "active" {
		t.Fatalf("round-trip mismatch: got %q", got)
	}

	if n, err := rdb.Incr(ctx, "hits").Result(); err != nil || n != 1 {
		t.Fatalf("INCR: n=%d err=%v", n, err)
	}

	_ = rdb.Close()

	// 4. Delete the cluster — the real Redis server is torn down.
	if _, err := client.DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
		CacheClusterId: aws.String(clusterID),
	}); err != nil {
		t.Fatalf("DeleteCacheCluster: %v", err)
	}

	// Fail fast (no retry spam): the endpoint should now be unreachable.
	gone := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: 300 * time.Millisecond})
	defer gone.Close()

	if err := gone.Ping(ctx).Err(); err == nil {
		t.Fatal("expected connection to the deleted cluster to fail")
	}
}
