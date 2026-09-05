package objectstorage

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

// engineWired reports whether object bytes live in a real storage engine
// rather than on the heap.
func (m *Mock) engineWired() bool { return m.opts.StorageEngine != nil }

// engineRef addresses one object version in the engine. OCI keeps full version
// chains, so the version id is part of the address the way S3's is.
func engineRef(bucket, key, version string) config.StorageRef {
	return config.StorageRef{Bucket: bucket, Key: key, Version: version}
}

// engineStore writes an object's bytes to the engine. Callers must not hold mu:
// the engine is real I/O.
func (m *Mock) engineStore(ctx context.Context, bucket string, obj *objectData) error {
	return storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
		Bucket:      bucket,
		Key:         obj.Name,
		Version:     obj.VersionID,
		Data:        obj.Data,
		ContentType: obj.ContentType,
		Metadata:    obj.Metadata,
	})
}

// offloadLocked hands a freshly stored object's bytes to the engine and drops
// the heap copy. It runs under the write lock rather than after it, so no
// reader ever observes an object whose bytes are in neither place. Callers hold
// mu for writing.
func (m *Mock) offloadLocked(ctx context.Context, bkt *bucketData, bucket string, obj *objectData) error {
	if !m.engineWired() {
		return nil
	}

	if err := m.engineStore(ctx, bucket, obj); err != nil {
		return err
	}

	dropBytesLocked(bkt, obj)

	return nil
}

// dropBytesLocked releases the in-memory copy of an object and of the version
// record that shares its bytes. Callers hold mu for writing.
func dropBytesLocked(bkt *bucketData, obj *objectData) {
	obj.Data = nil

	for _, v := range bkt.versions[obj.Name] {
		if v.versionID == obj.VersionID {
			v.data = nil
		}
	}
}

// purgeLocked removes a deleted object's engine bytes. A delete marker on a
// versioned bucket keeps the prior versions readable, so only an outright
// removal or a suspended-bucket null overwrite purges. Failures are ignored:
// an idempotent delete must not fail because the bytes were already gone.
// Callers hold mu for writing.
func (m *Mock) purgeLocked(ctx context.Context, bucket, key, versionID string, marker bool) {
	if !m.engineWired() {
		return
	}

	if marker && versionID != nullVersionID {
		return
	}

	_ = storageengine.Delete(ctx, m.opts.StorageEngine, engineRef(bucket, key, versionID))
}

// engineLoad returns the bytes for a version, preferring the in-memory copy so
// an unwired emulator behaves exactly as before.
func (m *Mock) engineLoad(ctx context.Context, ref config.StorageRef, inMemory []byte) ([]byte, error) {
	if !m.engineWired() || inMemory != nil {
		return inMemory, nil
	}

	data, ok, err := storageengine.Get(ctx, m.opts.StorageEngine, ref)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, nil
	}

	return data, nil
}
