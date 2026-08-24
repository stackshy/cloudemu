package elasticache_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	cachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newReplicationGroupClient(t *testing.T) *awselasticache.Client {
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

// A caller builds its Redis connection string from
// NodeGroups[0].PrimaryEndpoint.Address. If the node group or endpoint is
// missing the provision "succeeds" and hands back an unusable cache, so the
// endpoint — not just the 200 — is what this pins.
func TestReplicationGroupSDKRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newReplicationGroupClient(t)

	created, err := c.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("repl-1"),
		ReplicationGroupDescription: aws.String("primary with replicas"),
		CacheNodeType:               aws.String("cache.t3.micro"),
		Engine:                      aws.String("redis"),
		NumCacheClusters:            aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	if got := aws.ToString(created.ReplicationGroup.ReplicationGroupId); got != "repl-1" {
		t.Errorf("id = %q, want repl-1", got)
	}

	desc, err := c.DescribeReplicationGroups(ctx, &awselasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: aws.String("repl-1"),
	})
	if err != nil {
		t.Fatalf("DescribeReplicationGroups: %v", err)
	}

	if len(desc.ReplicationGroups) != 1 {
		t.Fatalf("describe = %d groups, want 1", len(desc.ReplicationGroups))
	}

	rg := desc.ReplicationGroups[0]

	if aws.ToString(rg.Status) != "available" {
		t.Errorf("status = %q, want available", aws.ToString(rg.Status))
	}

	if len(rg.NodeGroups) == 0 {
		t.Fatalf("no node groups: %+v", rg)
	}

	if rg.NodeGroups[0].PrimaryEndpoint == nil {
		t.Fatalf("no primary endpoint: %+v", rg.NodeGroups[0])
	}

	if addr := aws.ToString(rg.NodeGroups[0].PrimaryEndpoint.Address); addr == "" {
		t.Error("primary endpoint address is empty")
	}

	if port := aws.ToInt32(rg.NodeGroups[0].PrimaryEndpoint.Port); port != 6379 {
		t.Errorf("primary endpoint port = %d, want 6379", port)
	}

	if _, err := c.DeleteReplicationGroup(ctx, &awselasticache.DeleteReplicationGroupInput{
		ReplicationGroupId: aws.String("repl-1"),
	}); err != nil {
		t.Fatalf("DeleteReplicationGroup: %v", err)
	}

	if _, err := c.DescribeReplicationGroups(ctx,
		&awselasticache.DescribeReplicationGroupsInput{
			ReplicationGroupId: aws.String("repl-1"),
		}); err == nil {
		t.Error("describe after delete should fail")
	}
}

// TestDeleteReplicationGroupFinalSnapshot guards that DeleteReplicationGroup
// with FinalSnapshotIdentifier takes a final snapshot before deletion — real
// ElastiCache does, and the snapshot then shows up in DescribeSnapshots.
func TestDeleteReplicationGroupFinalSnapshot(t *testing.T) {
	ctx := context.Background()
	c := newReplicationGroupClient(t)

	if _, err := c.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("drg"),
		ReplicationGroupDescription: aws.String("group to snapshot on delete"),
		CacheNodeType:               aws.String("cache.t3.micro"),
		Engine:                      aws.String("redis"),
	}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	if _, err := c.DeleteReplicationGroup(ctx, &awselasticache.DeleteReplicationGroupInput{
		ReplicationGroupId:      aws.String("drg"),
		FinalSnapshotIdentifier: aws.String("finalsnap"),
	}); err != nil {
		t.Fatalf("DeleteReplicationGroup: %v", err)
	}

	got, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
		SnapshotName: aws.String("finalsnap"),
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}

	if len(got.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1 (final snapshot on delete)", len(got.Snapshots))
	}

	if id := aws.ToString(got.Snapshots[0].ReplicationGroupId); id != "drg" {
		t.Errorf("snapshot ReplicationGroupId = %q, want drg", id)
	}
}

// TestDeleteReplicationGroupRetainPrimary guards that RetainPrimaryCluster keeps
// the primary node group as a standalone cache cluster instead of tearing it
// down.
func TestDeleteReplicationGroupRetainPrimary(t *testing.T) {
	ctx := context.Background()
	c := newReplicationGroupClient(t)

	if _, err := c.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("keeprg"),
		ReplicationGroupDescription: aws.String("retain primary on delete"),
		CacheNodeType:               aws.String("cache.t3.micro"),
		Engine:                      aws.String("redis"),
	}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	if _, err := c.DeleteReplicationGroup(ctx, &awselasticache.DeleteReplicationGroupInput{
		ReplicationGroupId:   aws.String("keeprg"),
		RetainPrimaryCluster: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteReplicationGroup: %v", err)
	}

	got, err := c.DescribeCacheClusters(ctx, &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String("keeprg"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	if len(got.CacheClusters) != 1 {
		t.Fatalf("retained clusters = %d, want 1", len(got.CacheClusters))
	}
}

// Callers string-match these two codes: AlreadyExists to make a re-run
// idempotent, NotFoundFault to treat an absent group as already deleted.
// Losing either turns a safe path into a failure.
func TestReplicationGroupErrorCodes(t *testing.T) {
	ctx := context.Background()
	c := newReplicationGroupClient(t)

	in := &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("dup-rg"),
		ReplicationGroupDescription: aws.String("first"),
		NumCacheClusters:            aws.Int32(1),
	}

	if _, err := c.CreateReplicationGroup(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateReplicationGroup(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "ReplicationGroupAlreadyExists") {
		t.Errorf("duplicate must report ReplicationGroupAlreadyExists, got: %v", err)
	}

	_, err = c.DeleteReplicationGroup(ctx, &awselasticache.DeleteReplicationGroupInput{
		ReplicationGroupId: aws.String("never-existed"),
	})
	if err == nil || !strings.Contains(err.Error(), "ReplicationGroupNotFoundFault") {
		t.Errorf("delete of absent group must report ReplicationGroupNotFoundFault, got: %v", err)
	}
}

func TestModifyReplicationGroup(t *testing.T) {
	ctx := context.Background()
	c := newReplicationGroupClient(t)

	if _, err := c.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("scale-rg"),
		ReplicationGroupDescription: aws.String("scale me"),
		NumCacheClusters:            aws.Int32(1),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := c.ModifyReplicationGroup(ctx, &awselasticache.ModifyReplicationGroupInput{
		ReplicationGroupId: aws.String("scale-rg"),
	}); err != nil {
		t.Fatalf("ModifyReplicationGroup: %v", err)
	}

	if _, err := c.ModifyReplicationGroup(ctx, &awselasticache.ModifyReplicationGroupInput{
		ReplicationGroupId: aws.String("nope"),
	}); err == nil {
		t.Error("modifying an unknown group should fail")
	}
}

// Same reasoning as RDS: the SDK matches on the error CODE, not the message.
// A caller checking for ReplicationGroupAlreadyExistsFault would not have
// matched a generic CacheClusterAlreadyExists.
func TestReplicationGroupErrorsAreTyped(t *testing.T) {
	ctx := context.Background()
	c := newReplicationGroupClient(t)

	in := &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("typed-rg"),
		ReplicationGroupDescription: aws.String("first"),
		NumCacheClusters:            aws.Int32(1),
	}

	if _, err := c.CreateReplicationGroup(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	var exists *cachetypes.ReplicationGroupAlreadyExistsFault
	if _, err := c.CreateReplicationGroup(ctx, in); !errors.As(err, &exists) {
		t.Errorf("duplicate must surface ReplicationGroupAlreadyExistsFault, got %T: %v", err, err)
	}

	var missing *cachetypes.ReplicationGroupNotFoundFault
	if _, err := c.DeleteReplicationGroup(ctx, &awselasticache.DeleteReplicationGroupInput{
		ReplicationGroupId: aws.String("never-made"),
	}); !errors.As(err, &missing) {
		t.Errorf("missing group must surface ReplicationGroupNotFoundFault, got %T: %v", err, err)
	}
}
