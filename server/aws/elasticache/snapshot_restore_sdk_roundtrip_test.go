package elasticache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
)

// TestSnapshotRestoreCopyDeleteWorldCase drives the full real-user snapshot
// lifecycle against the wire server with the aws-sdk-go-v2 ElastiCache client:
// create → snapshot → copy → restore-into-a-new-cluster → mutate the source →
// prove copy/restore are unaffected → delete. It is the end-to-end proof for
// the ElastiCache snapshot restore/copy/delete surface.
func TestSnapshotRestoreCopyDeleteWorldCase(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	mustCreateCluster(t, c, "src-cluster")

	if _, err := c.CreateSnapshot(ctx, &awselasticache.CreateSnapshotInput{
		SnapshotName:   aws.String("snap-1"),
		CacheClusterId: aws.String("src-cluster"),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// Copy the snapshot to a new name; both must then exist independently.
	copied, err := c.CopySnapshot(ctx, &awselasticache.CopySnapshotInput{
		SourceSnapshotName: aws.String("snap-1"),
		TargetSnapshotName: aws.String("snap-copy"),
	})
	if err != nil {
		t.Fatalf("CopySnapshot: %v", err)
	}
	if got := aws.ToString(copied.Snapshot.SnapshotSource); got != "copied" {
		t.Errorf("copy SnapshotSource = %q, want copied", got)
	}
	if got := aws.ToString(copied.Snapshot.CacheNodeType); got != "cache.t3.micro" {
		t.Errorf("copy CacheNodeType = %q, want cache.t3.micro", got)
	}

	all, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}
	if len(all.Snapshots) != 2 {
		t.Fatalf("after copy DescribeSnapshots returned %d, want 2", len(all.Snapshots))
	}

	// Restore a brand-new cluster from the copy; its config seeds from the
	// snapshot even though the request names no engine/node-type.
	if _, err := c.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("restored-cluster"),
		SnapshotName:   aws.String("snap-copy"),
	}); err != nil {
		t.Fatalf("restore CreateCacheCluster: %v", err)
	}

	assertClusterShape(t, c, "restored-cluster", "redis", "cache.t3.micro")

	// Mutate the ORIGINAL cluster after snapshot+copy+restore. Deep-copy
	// independence means neither snapshot nor the restored cluster changes.
	if _, err := c.ModifyCacheCluster(ctx, &awselasticache.ModifyCacheClusterInput{
		CacheClusterId:   aws.String("src-cluster"),
		CacheNodeType:    aws.String("cache.r6g.large"),
		ApplyImmediately: aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyCacheCluster: %v", err)
	}

	for _, name := range []string{"snap-1", "snap-copy"} {
		got, derr := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
			SnapshotName: aws.String(name),
		})
		if derr != nil {
			t.Fatalf("DescribeSnapshots(%s): %v", name, derr)
		}
		if nt := aws.ToString(got.Snapshots[0].CacheNodeType); nt != "cache.t3.micro" {
			t.Errorf("snapshot %s CacheNodeType = %q after source mutation, want cache.t3.micro", name, nt)
		}
	}
	assertClusterShape(t, c, "restored-cluster", "redis", "cache.t3.micro")

	// Delete one snapshot; the other survives and the deleted one 404s.
	if _, err := c.DeleteSnapshot(ctx, &awselasticache.DeleteSnapshotInput{
		SnapshotName: aws.String("snap-1"),
	}); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	_, err = c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
		SnapshotName: aws.String("snap-1"),
	})
	var notFound *ectypes.SnapshotNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeSnapshots(snap-1) after delete = %v, want SnapshotNotFoundFault", err)
	}

	survivor, err := c.DescribeSnapshots(ctx, &awselasticache.DescribeSnapshotsInput{
		SnapshotName: aws.String("snap-copy"),
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots(snap-copy): %v", err)
	}
	if len(survivor.Snapshots) != 1 {
		t.Fatalf("surviving snapshot count = %d, want 1", len(survivor.Snapshots))
	}
}

func TestCopySnapshotMissingSourceReturnsSnapshotNotFoundFault(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	_, err := c.CopySnapshot(ctx, &awselasticache.CopySnapshotInput{
		SourceSnapshotName: aws.String("ghost"),
		TargetSnapshotName: aws.String("dst"),
	})
	var notFound *ectypes.SnapshotNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("CopySnapshot missing source = %v, want SnapshotNotFoundFault", err)
	}
}

func TestCopySnapshotDuplicateTargetReturnsSnapshotAlreadyExistsFault(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	mustCreateCluster(t, c, "cdup")
	for _, n := range []string{"s1", "s2"} {
		if _, err := c.CreateSnapshot(ctx, &awselasticache.CreateSnapshotInput{
			SnapshotName:   aws.String(n),
			CacheClusterId: aws.String("cdup"),
		}); err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", n, err)
		}
	}

	_, err := c.CopySnapshot(ctx, &awselasticache.CopySnapshotInput{
		SourceSnapshotName: aws.String("s1"),
		TargetSnapshotName: aws.String("s2"),
	})
	var already *ectypes.SnapshotAlreadyExistsFault
	if !errors.As(err, &already) {
		t.Fatalf("CopySnapshot duplicate target = %v, want SnapshotAlreadyExistsFault", err)
	}
}

func TestDeleteSnapshotMissingReturnsSnapshotNotFoundFault(t *testing.T) {
	ctx := context.Background()
	c := newSnapshotClient(t)

	_, err := c.DeleteSnapshot(ctx, &awselasticache.DeleteSnapshotInput{
		SnapshotName: aws.String("ghost"),
	})
	var notFound *ectypes.SnapshotNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("DeleteSnapshot missing = %v, want SnapshotNotFoundFault", err)
	}
}

// assertClusterShape reads a cluster back through DescribeCacheClusters and
// asserts its engine and node type.
func assertClusterShape(t *testing.T, c *awselasticache.Client, id, engine, nodeType string) {
	t.Helper()

	out, err := c.DescribeCacheClusters(context.Background(), &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters(%s): %v", id, err)
	}
	if len(out.CacheClusters) != 1 {
		t.Fatalf("DescribeCacheClusters(%s) returned %d, want 1", id, len(out.CacheClusters))
	}

	cl := out.CacheClusters[0]
	if got := aws.ToString(cl.Engine); got != engine {
		t.Errorf("cluster %s Engine = %q, want %q", id, got, engine)
	}
	if got := aws.ToString(cl.CacheNodeType); got != nodeType {
		t.Errorf("cluster %s CacheNodeType = %q, want %q", id, got, nodeType)
	}
}
