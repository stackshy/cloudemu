package objectstorage_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// fakeStorageEngine is a version-aware in-memory config.StorageEngine, so a
// test can assert which bytes reached the seam and under which version.
type fakeStorageEngine struct {
	mu   sync.Mutex
	data map[string][]byte
	err  error
}

func newFakeStorageEngine() *fakeStorageEngine {
	return &fakeStorageEngine{data: make(map[string][]byte)}
}

func refKey(ref config.StorageRef) string {
	return ref.Bucket + "\x00" + ref.Key + "\x00" + ref.Version
}

//nolint:gocritic // obj is the by-value DTO defined by the StorageEngine contract
func (f *fakeStorageEngine) Put(_ context.Context, obj config.StorageObject) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}

	f.data[refKey(config.StorageRef{Bucket: obj.Bucket, Key: obj.Key, Version: obj.Version})] =
		append([]byte(nil), obj.Data...)

	return nil
}

func (f *fakeStorageEngine) Get(_ context.Context, ref config.StorageRef) (config.StorageObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	b, ok := f.data[refKey(ref)]
	if !ok {
		return config.StorageObject{}, assert.AnError
	}

	return config.StorageObject{
		Bucket: ref.Bucket, Key: ref.Key, Version: ref.Version, Data: append([]byte(nil), b...),
	}, nil
}

func (f *fakeStorageEngine) Delete(_ context.Context, ref config.StorageRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.data, refKey(ref))

	return nil
}

func (f *fakeStorageEngine) Copy(ctx context.Context, dst, src config.StorageRef) error {
	obj, err := f.Get(ctx, src)
	if err != nil {
		return err
	}

	return f.Put(ctx, config.StorageObject{Bucket: dst.Bucket, Key: dst.Key, Version: dst.Version, Data: obj.Data})
}

func (f *fakeStorageEngine) has(bucket, key, version string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.data[refKey(config.StorageRef{Bucket: bucket, Key: key, Version: version})]

	return ok
}

func (f *fakeStorageEngine) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.data)
}

func newEngineMock(t *testing.T, eng config.StorageEngine) *objectstorage.Mock {
	t.Helper()

	return objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithStorageEngine(eng),
	))
}

func TestStorageEnginePutGetRoundTrip(t *testing.T) {
	eng := newFakeStorageEngine()
	m := newEngineMock(t, eng)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.PutObject(ctx, testBucket, "a.txt", []byte("hello engine"), "text/plain", nil))
	assert.True(t, eng.has(testBucket, "a.txt", ""), "the bytes reached the engine")

	got, err := m.GetObject(ctx, testBucket, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello engine"), got.Data)

	// Metadata survives the offload: Head and List read Size, not len(Data).
	head, err := m.HeadObject(ctx, testBucket, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(12), head.Size)

	list, err := m.ListObjects(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list.Objects, 1)
	assert.Equal(t, int64(12), list.Objects[0].Size)

	details, err := m.BucketDetails(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, int64(12), details.ApproximateSize)

	require.NoError(t, m.DeleteObject(ctx, testBucket, "a.txt"))
	assert.Zero(t, eng.len(), "delete purges the engine bytes")
}

func TestStorageEngineCopyAndRename(t *testing.T) {
	eng := newFakeStorageEngine()
	m := newEngineMock(t, eng)
	ctx := context.Background()
	newBucket(t, m, testBucket)
	newBucket(t, m, "bucket-b")

	require.NoError(t, m.PutObject(ctx, testBucket, "a.txt", []byte("payload"), "text/plain", nil))

	require.NoError(t, m.CopyObject(ctx, "bucket-b", "copied.txt",
		driver.CopySource{Bucket: testBucket, Key: "a.txt"}))
	assert.True(t, eng.has("bucket-b", "copied.txt", ""))

	copied, err := m.GetObject(ctx, "bucket-b", "copied.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), copied.Data)

	moved, err := m.RenameObject(ctx, testBucket, "a.txt", "b.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(7), moved.Size)
	assert.True(t, eng.has(testBucket, "b.txt", ""))
	assert.False(t, eng.has(testBucket, "a.txt", ""), "the source bytes are purged")

	renamed, err := m.GetObject(ctx, testBucket, "b.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), renamed.Data)
}

func TestStorageEngineVersionedRoundTrip(t *testing.T) {
	eng := newFakeStorageEngine()
	m := newEngineMock(t, eng)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v1"), "text/plain", nil))

	first, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)

	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v2"), "text/plain", nil))

	second, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, 2, eng.len(), "each version's bytes live at their own reference")

	old, err := m.GetObjectVersion(ctx, testBucket, "k", first.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), old.Data)

	oldHead, err := m.HeadObjectVersion(ctx, testBucket, "k", first.VersionID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), oldHead.Size)

	// A top-level delete on a versioned bucket appends a delete marker; the
	// prior versions' bytes must survive it.
	_, marker, err := m.DeleteObjectVersion(ctx, testBucket, "k", "")
	require.NoError(t, err)
	require.True(t, marker)
	assert.Equal(t, 2, eng.len(), "a delete marker purges nothing")

	still, err := m.GetObjectVersion(ctx, testBucket, "k", first.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), still.Data)

	// Removing a version by id does purge that version's bytes.
	_, _, err = m.DeleteObjectVersion(ctx, testBucket, "k", second.VersionID)
	require.NoError(t, err)
	assert.False(t, eng.has(testBucket, "k", second.VersionID))
	assert.True(t, eng.has(testBucket, "k", first.VersionID))
}

func TestStorageEngineSuspendedDeletePurgesNullBytes(t *testing.T) {
	eng := newFakeStorageEngine()
	m := newEngineMock(t, eng)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningSuspended))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v1"), "text/plain", nil))
	assert.True(t, eng.has(testBucket, "k", "null"))

	_, marker, err := m.DeleteObjectVersion(ctx, testBucket, "k", "")
	require.NoError(t, err)
	require.True(t, marker)
	assert.False(t, eng.has(testBucket, "k", "null"), "the null version is overwritten, so its bytes go")
}

func TestStorageEngineMultipartRoutesAssembledBytes(t *testing.T) {
	eng := newFakeStorageEngine()
	m := newEngineMock(t, eng)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	up, err := m.CreateMultipartUpload(ctx, testBucket, "big", "application/octet-stream")
	require.NoError(t, err)

	_, err = m.UploadPart(ctx, testBucket, "big", up.UploadID, 1, []byte("aaa"))
	require.NoError(t, err)
	_, err = m.UploadPart(ctx, testBucket, "big", up.UploadID, 2, []byte("bbb"))
	require.NoError(t, err)

	assert.Zero(t, eng.len(), "parts stay on the heap until the upload commits")

	require.NoError(t, m.CompleteMultipartUpload(ctx, testBucket, "big", up.UploadID,
		[]driver.UploadPart{{PartNumber: 1}, {PartNumber: 2}}))
	assert.True(t, eng.has(testBucket, "big", ""))

	got, err := m.GetObject(ctx, testBucket, "big")
	require.NoError(t, err)
	assert.Equal(t, []byte("aaabbb"), got.Data)
}

// An engine failure fails the write rather than leaving the emulator reporting
// an object whose bytes are in neither the engine nor memory.
func TestStorageEngineFailureFailsThePut(t *testing.T) {
	eng := newFakeStorageEngine()
	eng.err = assert.AnError
	m := newEngineMock(t, eng)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	err := m.PutObject(ctx, testBucket, "a.txt", []byte("x"), "text/plain", nil)
	require.Error(t, err)
	assert.Equal(t, cerrors.Internal, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "storage engine")
}
