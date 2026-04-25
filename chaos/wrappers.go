package chaos

import (
	"context"
	"time"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Driver-wrapper middleware. Each Wrap* takes a real driver and returns one
// that consults the chaos engine before delegating. The same wrapped driver
// can be handed to either:
//   - the portable API (e.g. storage.NewBucket(wrapped, ...)), or
//   - the SDK-compat HTTP server (awsserver.Drivers{S3: wrapped}).
//
// Both consumption paths see the same chaos behavior, which is the whole
// point: a real aws-sdk-go-v2 client over HTTP experiences the same outage
// as a Go-API caller.

// applyChaos is the shared "consult engine, sleep, maybe error" routine.
func applyChaos(ctx context.Context, e *Engine, service, op string) error {
	eff := e.Check(service, op)
	if eff.Latency > 0 {
		select {
		case <-time.After(eff.Latency):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return eff.Error
}

// chaosBucket embeds the inner driver so methods we don't override fall
// through automatically — keeps the wrapper short.
type chaosBucket struct {
	storagedriver.Bucket
	engine *Engine
}

// WrapBucket returns a storage driver that consults engine on the most-used
// data-plane operations. Less-used ops (lifecycle, multipart, tagging,
// versioning, CORS, encryption, policies, presigned URLs) delegate through
// without chaos for now — Phase 2 can broaden coverage.
func WrapBucket(inner storagedriver.Bucket, engine *Engine) storagedriver.Bucket {
	return &chaosBucket{Bucket: inner, engine: engine}
}

func (c *chaosBucket) CreateBucket(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "storage", "CreateBucket"); err != nil {
		return err
	}

	return c.Bucket.CreateBucket(ctx, name)
}

func (c *chaosBucket) DeleteBucket(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "storage", "DeleteBucket"); err != nil {
		return err
	}

	return c.Bucket.DeleteBucket(ctx, name)
}

func (c *chaosBucket) ListBuckets(ctx context.Context) ([]storagedriver.BucketInfo, error) {
	if err := applyChaos(ctx, c.engine, "storage", "ListBuckets"); err != nil {
		return nil, err
	}

	return c.Bucket.ListBuckets(ctx)
}

func (c *chaosBucket) PutObject(
	ctx context.Context, bucket, key string, data []byte,
	contentType string, metadata map[string]string,
) error {
	if err := applyChaos(ctx, c.engine, "storage", "PutObject"); err != nil {
		return err
	}

	return c.Bucket.PutObject(ctx, bucket, key, data, contentType, metadata)
}

func (c *chaosBucket) GetObject(ctx context.Context, bucket, key string) (*storagedriver.Object, error) {
	if err := applyChaos(ctx, c.engine, "storage", "GetObject"); err != nil {
		return nil, err
	}

	return c.Bucket.GetObject(ctx, bucket, key)
}

func (c *chaosBucket) HeadObject(ctx context.Context, bucket, key string) (*storagedriver.ObjectInfo, error) {
	if err := applyChaos(ctx, c.engine, "storage", "HeadObject"); err != nil {
		return nil, err
	}

	return c.Bucket.HeadObject(ctx, bucket, key)
}

func (c *chaosBucket) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := applyChaos(ctx, c.engine, "storage", "DeleteObject"); err != nil {
		return err
	}

	return c.Bucket.DeleteObject(ctx, bucket, key)
}

func (c *chaosBucket) ListObjects(ctx context.Context, bucket string, opts storagedriver.ListOptions) (*storagedriver.ListResult, error) {
	if err := applyChaos(ctx, c.engine, "storage", "ListObjects"); err != nil {
		return nil, err
	}

	return c.Bucket.ListObjects(ctx, bucket, opts)
}

func (c *chaosBucket) CopyObject(ctx context.Context, dstBucket, dstKey string, src storagedriver.CopySource) error {
	if err := applyChaos(ctx, c.engine, "storage", "CopyObject"); err != nil {
		return err
	}

	return c.Bucket.CopyObject(ctx, dstBucket, dstKey, src)
}

// chaosCompute wraps a compute driver. We only intercept the lifecycle ops
// that real applications most often exercise; the rest delegate through.
type chaosCompute struct {
	computedriver.Compute
	engine *Engine
}

// WrapCompute returns a compute driver that consults engine on instance ops.
func WrapCompute(inner computedriver.Compute, engine *Engine) computedriver.Compute {
	return &chaosCompute{Compute: inner, engine: engine}
}

//nolint:gocritic // cfg is a value type by interface contract; mirroring keeps signatures identical
func (c *chaosCompute) RunInstances(
	ctx context.Context, cfg computedriver.InstanceConfig, count int,
) ([]computedriver.Instance, error) {
	if err := applyChaos(ctx, c.engine, "compute", "RunInstances"); err != nil {
		return nil, err
	}

	return c.Compute.RunInstances(ctx, cfg, count)
}

func (c *chaosCompute) StartInstances(ctx context.Context, ids []string) error {
	if err := applyChaos(ctx, c.engine, "compute", "StartInstances"); err != nil {
		return err
	}

	return c.Compute.StartInstances(ctx, ids)
}

func (c *chaosCompute) StopInstances(ctx context.Context, ids []string) error {
	if err := applyChaos(ctx, c.engine, "compute", "StopInstances"); err != nil {
		return err
	}

	return c.Compute.StopInstances(ctx, ids)
}

func (c *chaosCompute) RebootInstances(ctx context.Context, ids []string) error {
	if err := applyChaos(ctx, c.engine, "compute", "RebootInstances"); err != nil {
		return err
	}

	return c.Compute.RebootInstances(ctx, ids)
}

func (c *chaosCompute) TerminateInstances(ctx context.Context, ids []string) error {
	if err := applyChaos(ctx, c.engine, "compute", "TerminateInstances"); err != nil {
		return err
	}

	return c.Compute.TerminateInstances(ctx, ids)
}

func (c *chaosCompute) DescribeInstances(
	ctx context.Context, ids []string, filters []computedriver.DescribeFilter,
) ([]computedriver.Instance, error) {
	if err := applyChaos(ctx, c.engine, "compute", "DescribeInstances"); err != nil {
		return nil, err
	}

	return c.Compute.DescribeInstances(ctx, ids, filters)
}

// chaosDatabase wraps a database driver.
type chaosDatabase struct {
	dbdriver.Database
	engine *Engine
}

// WrapDatabase returns a database driver that consults engine on item ops.
func WrapDatabase(inner dbdriver.Database, engine *Engine) dbdriver.Database {
	return &chaosDatabase{Database: inner, engine: engine}
}

func (c *chaosDatabase) PutItem(ctx context.Context, table string, item map[string]any) error {
	if err := applyChaos(ctx, c.engine, "database", "PutItem"); err != nil {
		return err
	}

	return c.Database.PutItem(ctx, table, item)
}

func (c *chaosDatabase) GetItem(ctx context.Context, table string, key map[string]any) (map[string]any, error) {
	if err := applyChaos(ctx, c.engine, "database", "GetItem"); err != nil {
		return nil, err
	}

	return c.Database.GetItem(ctx, table, key)
}

func (c *chaosDatabase) UpdateItem(ctx context.Context, input dbdriver.UpdateItemInput) (map[string]any, error) {
	if err := applyChaos(ctx, c.engine, "database", "UpdateItem"); err != nil {
		return nil, err
	}

	return c.Database.UpdateItem(ctx, input)
}

func (c *chaosDatabase) DeleteItem(ctx context.Context, table string, key map[string]any) error {
	if err := applyChaos(ctx, c.engine, "database", "DeleteItem"); err != nil {
		return err
	}

	return c.Database.DeleteItem(ctx, table, key)
}

//nolint:gocritic // input is a value type by interface contract
func (c *chaosDatabase) Query(
	ctx context.Context, input dbdriver.QueryInput,
) (*dbdriver.QueryResult, error) {
	if err := applyChaos(ctx, c.engine, "database", "Query"); err != nil {
		return nil, err
	}

	return c.Database.Query(ctx, input)
}

func (c *chaosDatabase) Scan(
	ctx context.Context, input dbdriver.ScanInput,
) (*dbdriver.QueryResult, error) {
	if err := applyChaos(ctx, c.engine, "database", "Scan"); err != nil {
		return nil, err
	}

	return c.Database.Scan(ctx, input)
}
