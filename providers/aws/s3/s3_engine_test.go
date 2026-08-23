package s3

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// fakeStorageEngine is a version-aware in-memory config.StorageEngine used to
// assert that object bytes (including per-version bytes) flow through the seam.
type fakeStorageEngine struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeStorageEngine() *fakeStorageEngine {
	return &fakeStorageEngine{data: make(map[string][]byte)}
}

func (f *fakeStorageEngine) refKey(ref config.StorageRef) string {
	return ref.Bucket + "\x00" + ref.Key + "\x00" + ref.Version
}

//nolint:gocritic // obj is the by-value DTO defined by the StorageEngine contract
func (f *fakeStorageEngine) Put(_ context.Context, obj config.StorageObject) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := append([]byte(nil), obj.Data...)
	f.data[f.refKey(config.StorageRef{Bucket: obj.Bucket, Key: obj.Key, Version: obj.Version})] = cp

	return nil
}

func (f *fakeStorageEngine) Get(_ context.Context, ref config.StorageRef) (config.StorageObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	b, ok := f.data[f.refKey(ref)]
	if !ok {
		return config.StorageObject{}, assert.AnError
	}

	return config.StorageObject{Bucket: ref.Bucket, Key: ref.Key, Version: ref.Version, Data: append([]byte(nil), b...)}, nil
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

	return f.Put(ctx, config.StorageObject{Bucket: dst.Bucket, Key: dst.Key, Version: dst.Version, Data: obj.Data})
}

func (f *fakeStorageEngine) has(ref config.StorageRef) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.data[f.refKey(ref)]

	return ok
}

func newEngineMock(eng config.StorageEngine) *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(clk), config.WithStorageEngine(eng)))
}

func TestS3StorageEnginePutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	body := []byte("hello engine")
	require.NoError(t, m.PutObject(ctx, "b1", "k1", body, "text/plain", map[string]string{"x": "y"}))

	// Bytes live in the engine (unversioned → version ""); the in-memory object drops its copy.
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k1"}))
	bkt, _ := m.buckets.Get("b1")
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

func TestS3StorageEngineHeadListCopyDelete(t *testing.T) {
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

	require.NoError(t, m.CopyObject(ctx, "b1", "k2", driver.CopySource{Bucket: "b1", Key: "k1"}))
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k2"}))
	cp, err := m.GetObject(ctx, "b1", "k2")
	require.NoError(t, err)
	assert.Equal(t, body, cp.Data)

	require.NoError(t, m.DeleteObject(ctx, "b1", "k1"))
	assert.False(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k1"}))
}

func TestS3StorageEngineMultipartRoutesBytes(t *testing.T) {
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
	got, err := m.GetObject(ctx, "b1", "big")
	require.NoError(t, err)
	assert.Equal(t, want, got.Data)
}

// TestS3StorageEngineVersionedRoundTrip exercises the highest-complexity paths:
// each version's bytes live in the engine under its version id, GetObjectVersion
// loads the right ones, and a top-level delete only stamps a marker (prior
// versions keep their bytes).
func TestS3StorageEngineVersionedRoundTrip(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	require.NoError(t, m.SetVersioningStatus(ctx, "b1", "Enabled"))
	require.NoError(t, m.PutObject(ctx, "b1", "k", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "b1", "k", []byte("v2"), "text/plain", nil))

	vers, err := m.ListObjectVersions(ctx, "b1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, vers.Versions, 2)

	// Each version's bytes resolve through the engine under its own version id.
	for _, v := range vers.Versions {
		got, gErr := m.GetObjectVersion(ctx, "b1", "k", v.VersionID)
		require.NoError(t, gErr)
		require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k", Version: v.VersionID}))
		assert.Contains(t, [][]byte{[]byte("v1"), []byte("v2")}, got.Data)
	}

	// Current object reads the latest version's bytes.
	cur, err := m.GetObject(ctx, "b1", "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), cur.Data)

	// A top-level delete only adds a delete marker — prior version bytes stay.
	require.NoError(t, m.DeleteObject(ctx, "b1", "k"))
	_, err = m.GetObject(ctx, "b1", "k")
	require.Error(t, err, "current read after delete marker is a 404")
	for _, v := range vers.Versions {
		require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k", Version: v.VersionID}), "prior version bytes retained")
	}
}

// TestS3StorageEngineSuspendedDeletePurgesNullBytes proves a suspended-bucket
// top-level delete purges the "null" object's engine bytes (no orphan).
func TestS3StorageEngineSuspendedDeletePurgesNullBytes(t *testing.T) {
	ctx := context.Background()
	eng := newFakeStorageEngine()
	m := newEngineMock(eng)

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	require.NoError(t, m.SetVersioningStatus(ctx, "b1", "Suspended"))
	require.NoError(t, m.PutObject(ctx, "b1", "k", []byte("nullbytes"), "text/plain", nil))
	require.True(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k", Version: "null"}))

	require.NoError(t, m.DeleteObject(ctx, "b1", "k"))
	assert.False(t, eng.has(config.StorageRef{Bucket: "b1", Key: "k", Version: "null"}), "suspended delete purges the null bytes")
}
