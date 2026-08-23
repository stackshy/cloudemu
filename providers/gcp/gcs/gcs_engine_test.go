package gcs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStorageEngine is an in-memory config.StorageEngine used to assert that
// object bytes flow through the engine seam when one is wired.
type fakeStorageEngine struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeStorageEngine() *fakeStorageEngine {
	return &fakeStorageEngine{data: make(map[string][]byte)}
}

func (f *fakeStorageEngine) refKey(ref config.StorageRef) string {
	return ref.Bucket + "/" + ref.Key
}

//nolint:gocritic // obj is the by-value DTO defined by the StorageEngine contract
func (f *fakeStorageEngine) Put(_ context.Context, obj config.StorageObject) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := make([]byte, len(obj.Data))
	copy(cp, obj.Data)
	f.data[f.refKey(config.StorageRef{Bucket: obj.Bucket, Key: obj.Key})] = cp

	return nil
}

func (f *fakeStorageEngine) Get(_ context.Context, ref config.StorageRef) (config.StorageObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	b, ok := f.data[f.refKey(ref)]
	if !ok {
		return config.StorageObject{}, assert.AnError
	}

	cp := make([]byte, len(b))
	copy(cp, b)

	return config.StorageObject{Bucket: ref.Bucket, Key: ref.Key, Data: cp}, nil
}

func (f *fakeStorageEngine) Delete(_ context.Context, ref config.StorageRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.data, f.refKey(ref))

	return nil
}

func (f *fakeStorageEngine) Copy(ctx context.Context, dst, src config.StorageRef) error {
	obj, err := f.Get(ctx, src)
	if err != nil {
		return err
	}

	return f.Put(ctx, config.StorageObject{Bucket: dst.Bucket, Key: dst.Key, Data: obj.Data})
}

func (f *fakeStorageEngine) has(ref config.StorageRef) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.data[f.refKey(ref)]

	return ok
}

func newEngineMock(eng config.StorageEngine) *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(clk),
		config.WithRegion("us-central1"),
		config.WithProjectID("test-project"),
		config.WithStorageEngine(eng),
	)

	return New(opts)
}

func TestStorageEnginePutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	body := []byte("hello engine")
	require.NoError(t, m.PutObject(ctx, "b1", "k1", body, "text/plain", map[string]string{"x": "y"}))

	// Bytes live in the engine; the in-memory object drops its Data copy.
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k1"}))

	bkt, ok := m.buckets.Get("b1")
	require.True(t, ok)
	obj, ok := bkt.objects.Get("k1")
	require.True(t, ok)
	assert.Nil(t, obj.Data)
	assert.Equal(t, int64(len(body)), obj.Size)

	got, err := m.GetObject(ctx, "b1", "k1")
	require.NoError(t, err)
	assert.Equal(t, body, got.Data)
	assert.Equal(t, int64(len(body)), got.Info.Size)
	assert.Equal(t, "text/plain", got.Info.ContentType)
}

func TestStorageEngineHeadAndListReportSize(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	body := []byte("sized-bytes")
	require.NoError(t, m.PutObject(ctx, "b1", "k1", body, "application/octet-stream", nil))

	head, err := m.HeadObject(ctx, "b1", "k1")
	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), head.Size)

	list, err := m.ListObjects(ctx, "b1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list.Objects, 1)
	assert.Equal(t, int64(len(body)), list.Objects[0].Size)
}

func TestStorageEngineCopyRoutesBytes(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	body := []byte("copy-me")
	require.NoError(t, m.PutObject(ctx, "b1", "src", body, "text/plain", nil))

	require.NoError(t, m.CopyObject(ctx, "b1", "dst", driver.CopySource{Bucket: "b1", Key: "src"}))
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "dst"}))

	bkt, _ := m.buckets.Get("b1")
	dst, ok := bkt.objects.Get("dst")
	require.True(t, ok)
	assert.Nil(t, dst.Data)
	assert.Equal(t, int64(len(body)), dst.Size)

	got, err := m.GetObject(ctx, "b1", "dst")
	require.NoError(t, err)
	assert.Equal(t, body, got.Data)
}

func TestStorageEngineDeleteRemovesBytes(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	require.NoError(t, m.PutObject(ctx, "b1", "k1", []byte("bye"), "text/plain", nil))
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k1"}))

	require.NoError(t, m.DeleteObject(ctx, "b1", "k1"))
	assert.False(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k1"}))
}

func TestStorageEngineMultipartRoutesBytes(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	up, err := m.CreateMultipartUpload(ctx, "b1", "big", "text/plain")
	require.NoError(t, err)

	p1, err := m.UploadPart(ctx, "b1", "big", up.UploadID, 1, []byte("part-one-"))
	require.NoError(t, err)
	p2, err := m.UploadPart(ctx, "b1", "big", up.UploadID, 2, []byte("part-two"))
	require.NoError(t, err)

	require.NoError(t, m.CompleteMultipartUpload(ctx, "b1", "big", up.UploadID,
		[]driver.UploadPart{{PartNumber: 1, ETag: p1.ETag}, {PartNumber: 2, ETag: p2.ETag}}))

	want := []byte("part-one-part-two")
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "big"}))

	bkt, _ := m.buckets.Get("b1")
	obj, ok := bkt.objects.Get("big")
	require.True(t, ok)
	assert.Nil(t, obj.Data)
	assert.Equal(t, int64(len(want)), obj.Size)

	got, err := m.GetObject(ctx, "b1", "big")
	require.NoError(t, err)
	assert.Equal(t, want, got.Data)
}
