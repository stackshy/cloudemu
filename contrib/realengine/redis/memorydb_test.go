package redis_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	goredis "github.com/redis/go-redis/v9"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	redisengine "github.com/stackshy/cloudemu/v2/contrib/realengine/redis"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestMemoryDBRedisE2E runs the real-user flow: create a MemoryDB cluster with
// the AWS SDK, read the cluster endpoint, connect to it with a real Redis
// client, run real commands, then delete the cluster — all against CloudEmu
// backed by a real in-process Redis (no Docker, no cloud account). MemoryDB is
// Redis-compatible, so it reuses the existing Redis CacheEngine.
func TestMemoryDBRedisE2E(t *testing.T) {
	eng := redisengine.New()
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

	client := memorydb.NewFromConfig(cfg, func(o *memorydb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const clusterName = "app-memorydb"

	// 1. Create the cluster — like `aws memorydb create-cluster`.
	out, err := client.CreateCluster(ctx, &memorydb.CreateClusterInput{
		ClusterName: aws.String(clusterName),
		NodeType:    aws.String("db.t4g.small"),
		ACLName:     aws.String("open-access"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// 2. Read the cluster endpoint the SDK reports — the real Redis address.
	if out.Cluster == nil || out.Cluster.ClusterEndpoint == nil {
		t.Fatalf("no cluster endpoint reported: %+v", out.Cluster)
	}

	ep := out.Cluster.ClusterEndpoint
	addr := fmt.Sprintf("%s:%d", aws.ToString(ep.Address), ep.Port)

	// 3. Connect with a real Redis client and run real commands.
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})

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
	if _, err := client.DeleteCluster(ctx, &memorydb.DeleteClusterInput{
		ClusterName: aws.String(clusterName),
	}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	// Fail fast (no retry spam): the endpoint should now be unreachable.
	gone := goredis.NewClient(&goredis.Options{Addr: addr, MaxRetries: -1, DialTimeout: 300 * time.Millisecond})
	defer gone.Close()

	if err := gone.Ping(ctx).Err(); err == nil {
		t.Fatal("expected connection to the deleted cluster to fail")
	}
}
