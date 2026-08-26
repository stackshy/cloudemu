package blobstorage

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// latestBlocks builds a Put Block List that references each id from the Latest
// source list (the azblob default).
func latestBlocks(ids ...string) []driver.BlockListEntry {
	entries := make([]driver.BlockListEntry, len(ids))
	for i, id := range ids {
		entries[i] = driver.BlockListEntry{ID: id, List: driver.BlockListLatest}
	}

	return entries
}

func TestStageAndCommitBlockList(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b1", []byte("foo")))
	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b2", []byte("bar")))

	info, err := m.CommitBlockList(ctx, "c1", "k1", latestBlocks("b1", "b2"), "text/plain", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(6), info.Size)

	obj, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "foobar", string(obj.Data))
	assert.Equal(t, "text/plain", obj.Info.ContentType)
}

func TestPutBlockBlobContentPropertiesRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	props := &driver.BlobProperties{
		ContentType: "text/plain", ContentEncoding: "gzip", CacheControl: "no-cache",
		ContentLanguage: "en-US", ContentDisposition: "attachment",
	}

	info, err := m.PutBlockBlob(ctx, "c1", "k1", []byte("payload"), props, nil)
	require.NoError(t, err)
	assert.Equal(t, "gzip", info.ContentEncoding)
	assert.Equal(t, "no-cache", info.CacheControl)
	assert.Equal(t, "en-US", info.ContentLanguage)
	assert.Equal(t, "attachment", info.ContentDisposition)

	obj, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "payload", string(obj.Data))
	assert.Equal(t, "gzip", obj.Info.ContentEncoding)
	assert.Equal(t, "en-US", obj.Info.ContentLanguage)
	assert.Equal(t, "attachment", obj.Info.ContentDisposition)
	assert.Equal(t, "no-cache", obj.Info.CacheControl)
}

func TestCommitBlockListReferencesCommittedBlocks(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b1", []byte("foo")))
	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b2", []byte("bar")))

	_, err := m.CommitBlockList(ctx, "c1", "k1", latestBlocks("b1", "b2"), "text/plain", nil, nil)
	require.NoError(t, err)

	// Stage one new block, then re-commit the two already-committed blocks
	// (referenced from the Committed source list) plus the new one.
	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b3", []byte("baz")))

	entries := []driver.BlockListEntry{
		{ID: "b1", List: driver.BlockListCommitted},
		{ID: "b2", List: driver.BlockListCommitted},
		{ID: "b3", List: driver.BlockListUncommitted},
	}

	info, err := m.CommitBlockList(ctx, "c1", "k1", entries, "text/plain", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(9), info.Size)

	obj, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "foobarbaz", string(obj.Data))
}

func TestCommitBlockListContentProperties(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b1", []byte("foo")))

	props := &driver.BlobProperties{ContentEncoding: "gzip", CacheControl: "max-age=42"}

	info, err := m.CommitBlockList(ctx, "c1", "k1", latestBlocks("b1"), "text/plain", props, nil)
	require.NoError(t, err)
	assert.Equal(t, "gzip", info.ContentEncoding)
	assert.Equal(t, "max-age=42", info.CacheControl)

	head, err := m.HeadObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "gzip", head.ContentEncoding)
	assert.Equal(t, "max-age=42", head.CacheControl)
}

func TestCommitBlockListMissingBlock(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.StageBlock(ctx, "c1", "k1", "b1", []byte("foo")))

	_, err := m.CommitBlockList(ctx, "c1", "k1", latestBlocks("b1", "missing"), "", nil, nil)
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestSetBlobMetadataPreservesContent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("payload"), "text/plain", nil))

	before, err := m.HeadObject(ctx, "c1", "k1")
	require.NoError(t, err)

	info, err := m.SetBlobMetadata(ctx, "c1", "k1", map[string]string{"a": "b"})
	require.NoError(t, err)
	assert.NotEqual(t, before.ETag, info.ETag, "metadata update must change the ETag")

	obj, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "payload", string(obj.Data))
	assert.Equal(t, "b", obj.Info.Metadata["a"])
}

func TestAppendBlockRequiresAppendBlob(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("block"), "text/plain", nil))

	_, _, _, err := m.AppendBlock(ctx, "c1", "k1", []byte("more"))
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))
}

func TestAppendBlobLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	_, err := m.CreateAppendBlob(ctx, "c1", "k1", "text/plain", nil)
	require.NoError(t, err)

	offset, count, _, err := m.AppendBlock(ctx, "c1", "k1", []byte("one"))
	require.NoError(t, err)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, 1, count)

	offset, count, _, err = m.AppendBlock(ctx, "c1", "k1", []byte("two"))
	require.NoError(t, err)
	assert.Equal(t, int64(3), offset)
	assert.Equal(t, 2, count)

	obj, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "onetwo", string(obj.Data))
	assert.Equal(t, blobTypeAppend, obj.Info.BlobType)
}

func TestSnapshotSurvivesOverwrite(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	// Two snapshots at the same (fake) instant must get distinct IDs.
	id1, _, err := m.CreateBlobSnapshot(ctx, "c1", "k1")
	require.NoError(t, err)
	id2, _, err := m.CreateBlobSnapshot(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2)

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v2"), "text/plain", nil))

	snap, err := m.GetBlobSnapshot(ctx, "c1", "k1", id1)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(snap.Data), "snapshot must survive base overwrite")

	base, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "v2", string(base.Data))
}
