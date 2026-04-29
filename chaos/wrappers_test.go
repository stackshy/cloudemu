package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	awsdynamo "github.com/stackshy/cloudemu/providers/aws/dynamodb"
	awsec2 "github.com/stackshy/cloudemu/providers/aws/ec2"
	awss3 "github.com/stackshy/cloudemu/providers/aws/s3"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// withChaos wraps a fresh AWS S3 mock with a chaos-applied engine.
func newChaosBucket(t *testing.T) (storagedriver.Bucket, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapBucket(awss3.New(config.NewOptions()), e), e
}

func TestWrapBucketCreateBucketChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()

	if err := b.CreateBucket(ctx, "ok"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if err := b.CreateBucket(ctx, "fail"); err == nil {
		t.Error("expected chaos error on CreateBucket")
	}
}

func TestWrapBucketDeleteBucketChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()

	_ = b.CreateBucket(ctx, "del")
	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if err := b.DeleteBucket(ctx, "del"); err == nil {
		t.Error("expected chaos error on DeleteBucket")
	}
}

func TestWrapBucketListBucketsChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()

	if _, err := b.ListBuckets(ctx); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if _, err := b.ListBuckets(ctx); err == nil {
		t.Error("expected chaos error on ListBuckets")
	}
}

func TestWrapBucketPutObjectChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "put")

	if err := b.PutObject(ctx, "put", "k", []byte("v"), "text/plain", nil); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if err := b.PutObject(ctx, "put", "k", []byte("v"), "text/plain", nil); err == nil {
		t.Error("expected chaos error on PutObject")
	}
}

func TestWrapBucketGetObjectChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "g")
	_ = b.PutObject(ctx, "g", "k", []byte("v"), "text/plain", nil)

	if _, err := b.GetObject(ctx, "g", "k"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if _, err := b.GetObject(ctx, "g", "k"); err == nil {
		t.Error("expected chaos error on GetObject")
	}
}

func TestWrapBucketHeadObjectChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "h")
	_ = b.PutObject(ctx, "h", "k", []byte("v"), "text/plain", nil)

	if _, err := b.HeadObject(ctx, "h", "k"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if _, err := b.HeadObject(ctx, "h", "k"); err == nil {
		t.Error("expected chaos error on HeadObject")
	}
}

func TestWrapBucketDeleteObjectChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "d")
	_ = b.PutObject(ctx, "d", "k", []byte("v"), "text/plain", nil)

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if err := b.DeleteObject(ctx, "d", "k"); err == nil {
		t.Error("expected chaos error on DeleteObject")
	}
}

func TestWrapBucketListObjectsChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "l")

	if _, err := b.ListObjects(ctx, "l", storagedriver.ListOptions{}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if _, err := b.ListObjects(ctx, "l", storagedriver.ListOptions{}); err == nil {
		t.Error("expected chaos error on ListObjects")
	}
}

func TestWrapBucketCopyObjectChaos(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "src")
	_ = b.CreateBucket(ctx, "dst")
	_ = b.PutObject(ctx, "src", "k", []byte("v"), "text/plain", nil)

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if err := b.CopyObject(ctx, "dst", "k", storagedriver.CopySource{Bucket: "src", Key: "k"}); err == nil {
		t.Error("expected chaos error on CopyObject")
	}
}

func TestWrapBucketLatencyApplied(t *testing.T) {
	b, e := newChaosBucket(t)
	ctx := context.Background()
	_ = b.CreateBucket(ctx, "lat")

	e.Apply(chaos.LatencySpike("storage", 50*time.Millisecond, time.Hour))

	start := time.Now()

	_ = b.PutObject(ctx, "lat", "k", []byte("x"), "text/plain", nil)

	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("expected ≥50ms latency, got %v", elapsed)
	}
}

func TestWrapBucketContextCancelDuringLatency(t *testing.T) {
	b, e := newChaosBucket(t)
	_ = b.CreateBucket(context.Background(), "ctx")

	e.Apply(chaos.LatencySpike("storage", 500*time.Millisecond, time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := b.PutObject(ctx, "ctx", "k", []byte("x"), "text/plain", nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// Compute wrapper

func newChaosCompute(t *testing.T) (interface {
	RunInstances(context.Context, struct {
		ImageID, InstanceType string
		Tags                  map[string]string
		SubnetID              string
		SecurityGroups        []string
		KeyName, UserData     string
	}, int) ([]struct{}, error)
}, *chaos.Engine) {
	t.Helper()
	// We don't need this helper — RunInstances signature doesn't match the
	// anonymous struct above. Tests below construct the wrapped driver
	// directly with the concrete provider type.
	return nil, nil
}

func TestWrapComputeRunInstancesChaos(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	c := chaos.WrapCompute(awsec2.New(config.NewOptions()), e)
	ctx := context.Background()

	cfg := computeInstanceConfigForWrappers()

	if _, err := c.RunInstances(ctx, cfg, 1); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if _, err := c.RunInstances(ctx, cfg, 1); err == nil {
		t.Error("expected chaos error on RunInstances")
	}
}

func TestWrapComputeStartInstancesChaos(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	c := chaos.WrapCompute(awsec2.New(config.NewOptions()), e)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if err := c.StartInstances(ctx, []string{"i-x"}); err == nil {
		t.Error("expected chaos error on StartInstances")
	}
}

func TestWrapComputeStopInstancesChaos(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	c := chaos.WrapCompute(awsec2.New(config.NewOptions()), e)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if err := c.StopInstances(ctx, []string{"i-x"}); err == nil {
		t.Error("expected chaos error on StopInstances")
	}
}

func TestWrapComputeRebootInstancesChaos(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	c := chaos.WrapCompute(awsec2.New(config.NewOptions()), e)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if err := c.RebootInstances(ctx, []string{"i-x"}); err == nil {
		t.Error("expected chaos error on RebootInstances")
	}
}

func TestWrapComputeTerminateInstancesChaos(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	c := chaos.WrapCompute(awsec2.New(config.NewOptions()), e)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if err := c.TerminateInstances(ctx, []string{"i-x"}); err == nil {
		t.Error("expected chaos error on TerminateInstances")
	}
}

func TestWrapComputeDescribeInstancesChaos(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	c := chaos.WrapCompute(awsec2.New(config.NewOptions()), e)
	ctx := context.Background()

	if _, err := c.DescribeInstances(ctx, nil, nil); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if _, err := c.DescribeInstances(ctx, nil, nil); err == nil {
		t.Error("expected chaos error on DescribeInstances")
	}
}

// Database wrapper

func newChaosDatabaseHelper(t *testing.T) (dbdriver.Database, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapDatabase(awsdynamo.New(config.NewOptions()), e), e
}

func TestWrapDatabasePutItemChaos(t *testing.T) {
	d, e := newChaosDatabaseHelper(t)
	ctx := context.Background()

	_ = d.CreateTable(ctx, dbdriver.TableConfig{Name: "t", PartitionKey: "id"})

	if err := d.PutItem(ctx, "t", map[string]any{"id": "k1"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("database", time.Hour))

	if err := d.PutItem(ctx, "t", map[string]any{"id": "k2"}); err == nil {
		t.Error("expected chaos error on PutItem")
	}
}

func TestWrapDatabaseGetItemChaos(t *testing.T) {
	d, e := newChaosDatabaseHelper(t)
	ctx := context.Background()

	_ = d.CreateTable(ctx, dbdriver.TableConfig{Name: "g", PartitionKey: "id"})
	_ = d.PutItem(ctx, "g", map[string]any{"id": "k"})

	if _, err := d.GetItem(ctx, "g", map[string]any{"id": "k"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("database", time.Hour))

	if _, err := d.GetItem(ctx, "g", map[string]any{"id": "k"}); err == nil {
		t.Error("expected chaos error on GetItem")
	}
}

func TestWrapDatabaseUpdateItemChaos(t *testing.T) {
	d, e := newChaosDatabaseHelper(t)
	ctx := context.Background()

	_ = d.CreateTable(ctx, dbdriver.TableConfig{Name: "u", PartitionKey: "id"})
	_ = d.PutItem(ctx, "u", map[string]any{"id": "k", "v": "old"})

	e.Apply(chaos.ServiceOutage("database", time.Hour))

	_, err := d.UpdateItem(ctx, dbdriver.UpdateItemInput{
		Table: "u",
		Key:   map[string]any{"id": "k"},
		Actions: []dbdriver.UpdateAction{
			{Action: "SET", Field: "v", Value: "new"},
		},
	})
	if err == nil {
		t.Error("expected chaos error on UpdateItem")
	}
}

func TestWrapDatabaseDeleteItemChaos(t *testing.T) {
	d, e := newChaosDatabaseHelper(t)
	ctx := context.Background()

	_ = d.CreateTable(ctx, dbdriver.TableConfig{Name: "del", PartitionKey: "id"})
	_ = d.PutItem(ctx, "del", map[string]any{"id": "k"})

	e.Apply(chaos.ServiceOutage("database", time.Hour))

	if err := d.DeleteItem(ctx, "del", map[string]any{"id": "k"}); err == nil {
		t.Error("expected chaos error on DeleteItem")
	}
}

func TestWrapDatabaseQueryChaos(t *testing.T) {
	d, e := newChaosDatabaseHelper(t)
	ctx := context.Background()

	_ = d.CreateTable(ctx, dbdriver.TableConfig{Name: "q", PartitionKey: "id"})

	e.Apply(chaos.ServiceOutage("database", time.Hour))

	if _, err := d.Query(ctx, dbdriver.QueryInput{Table: "q"}); err == nil {
		t.Error("expected chaos error on Query")
	}
}

func TestWrapDatabaseScanChaos(t *testing.T) {
	d, e := newChaosDatabaseHelper(t)
	ctx := context.Background()

	_ = d.CreateTable(ctx, dbdriver.TableConfig{Name: "s", PartitionKey: "id"})

	e.Apply(chaos.ServiceOutage("database", time.Hour))

	if _, err := d.Scan(ctx, dbdriver.ScanInput{Table: "s"}); err == nil {
		t.Error("expected chaos error on Scan")
	}
}

// Helper from chaos_test.go was here previously; we keep it co-located so
// wrapper tests are self-contained.
type computeConfigCompat = struct {
	ImageID        string
	InstanceType   string
	Tags           map[string]string
	SubnetID       string
	SecurityGroups []string
	KeyName        string
	UserData       string
}

// computeInstanceConfig reproduces the helper used elsewhere in the package
// so this file's tests don't depend on cross-file ordering.
//
// Re-declared with a wrapping function so we don't get a "duplicate name"
// build error (the compute config helper already lives in chaos_test.go).
func computeInstanceConfigForWrappers() computeConfigCompat {
	return computeConfigCompat{ImageID: "ami-test", InstanceType: "t2.micro"}
}

var _ = computeInstanceConfigForWrappers // referenced for documentation
