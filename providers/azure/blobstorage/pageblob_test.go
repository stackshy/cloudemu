package blobstorage

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageBlobCreateAndGetRanges(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	info, err := m.CreatePageBlob(ctx, "c1", "pb", 2048, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2048), info.Size)
	assert.Equal(t, blobTypePage, info.BlobType)

	ranges, size, err := m.GetPageRanges(ctx, "c1", "pb")
	require.NoError(t, err)
	assert.Equal(t, int64(2048), size)
	assert.Empty(t, ranges)

	// Write two non-adjacent pages: 0-511 and 1024-1535.
	_, err = m.PutPage(ctx, "c1", "pb", 0, 511, bytes.Repeat([]byte{1}, 512))
	require.NoError(t, err)
	_, err = m.PutPage(ctx, "c1", "pb", 1024, 1535, bytes.Repeat([]byte{2}, 512))
	require.NoError(t, err)

	ranges, _, err = m.GetPageRanges(ctx, "c1", "pb")
	require.NoError(t, err)
	require.Len(t, ranges, 2)
	assert.Equal(t, int64(0), ranges[0].Start)
	assert.Equal(t, int64(511), ranges[0].End)
	assert.Equal(t, int64(1024), ranges[1].Start)
	assert.Equal(t, int64(1535), ranges[1].End)

	// Content reflects the writes over a zero background.
	obj, err := m.GetObject(ctx, "c1", "pb")
	require.NoError(t, err)
	require.Len(t, obj.Data, 2048)
	assert.Equal(t, bytes.Repeat([]byte{1}, 512), obj.Data[0:512])
	assert.Equal(t, make([]byte, 512), obj.Data[512:1024])
	assert.Equal(t, bytes.Repeat([]byte{2}, 512), obj.Data[1024:1536])
}

func TestPageBlobClearAndCoalesce(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	_, err := m.CreatePageBlob(ctx, "c1", "pb", 2048, nil, nil)
	require.NoError(t, err)

	// Two adjacent pages coalesce into one range.
	_, err = m.PutPage(ctx, "c1", "pb", 0, 511, bytes.Repeat([]byte{1}, 512))
	require.NoError(t, err)
	_, err = m.PutPage(ctx, "c1", "pb", 512, 1023, bytes.Repeat([]byte{1}, 512))
	require.NoError(t, err)

	ranges, _, err := m.GetPageRanges(ctx, "c1", "pb")
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	assert.Equal(t, int64(0), ranges[0].Start)
	assert.Equal(t, int64(1023), ranges[0].End)

	// Clearing the first page splits the run and zeroes those bytes.
	_, err = m.ClearPage(ctx, "c1", "pb", 0, 511)
	require.NoError(t, err)

	ranges, _, err = m.GetPageRanges(ctx, "c1", "pb")
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	assert.Equal(t, int64(512), ranges[0].Start)
	assert.Equal(t, int64(1023), ranges[0].End)

	obj, err := m.GetObject(ctx, "c1", "pb")
	require.NoError(t, err)
	assert.Equal(t, make([]byte, 512), obj.Data[0:512])
}

func TestPageBlobRangesSurviveSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	_, err := m.CreatePageBlob(ctx, "c1", "pb", 2048, nil, nil)
	require.NoError(t, err)

	// Two non-adjacent written ranges plus distinct page contents.
	_, err = m.PutPage(ctx, "c1", "pb", 0, 511, bytes.Repeat([]byte{0xAB}, 512))
	require.NoError(t, err)
	_, err = m.PutPage(ctx, "c1", "pb", 1024, 1535, bytes.Repeat([]byte{0xCD}, 512))
	require.NoError(t, err)

	before, _, err := m.GetPageRanges(ctx, "c1", "pb")
	require.NoError(t, err)
	require.Len(t, before, 2)

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))

	// The written-range map survives the round-trip: exactly the same two ranges.
	after, size, err := restored.GetPageRanges(ctx, "c1", "pb")
	require.NoError(t, err)
	assert.Equal(t, int64(2048), size)
	assert.Equal(t, before, after)

	// And the bytes match.
	obj, err := restored.GetObject(ctx, "c1", "pb")
	require.NoError(t, err)
	require.Len(t, obj.Data, 2048)
	assert.Equal(t, bytes.Repeat([]byte{0xAB}, 512), obj.Data[0:512])
	assert.Equal(t, make([]byte, 512), obj.Data[512:1024])
	assert.Equal(t, bytes.Repeat([]byte{0xCD}, 512), obj.Data[1024:1536])
}

func TestPageBlobValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	// Non-512-multiple size is rejected.
	_, err := m.CreatePageBlob(ctx, "c1", "bad", 1000, nil, nil)
	require.Error(t, err)

	_, err = m.CreatePageBlob(ctx, "c1", "pb", 1024, nil, nil)
	require.NoError(t, err)

	// Misaligned range is rejected.
	_, err = m.PutPage(ctx, "c1", "pb", 10, 521, bytes.Repeat([]byte{1}, 512))
	require.Error(t, err)

	// Out-of-bounds range is rejected.
	_, err = m.PutPage(ctx, "c1", "pb", 1024, 1535, bytes.Repeat([]byte{1}, 512))
	require.Error(t, err)

	// A page op on a non-page (block) blob is rejected.
	require.NoError(t, m.PutObject(ctx, "c1", "block", []byte("x"), "text/plain", nil))
	_, err = m.PutPage(ctx, "c1", "block", 0, 511, bytes.Repeat([]byte{1}, 512))
	require.Error(t, err)

	_, _, err = m.GetPageRanges(ctx, "c1", "block")
	require.Error(t, err)
}
