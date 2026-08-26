package blobstorage

import (
	"context"
	"testing"

	driver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyObjectInheritsMetadata(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "src"))
	require.NoError(t, m.CreateBucket(ctx, "dst"))
	require.NoError(t, m.PutObject(ctx, "src", "k", []byte("data"), "text/plain", map[string]string{"orig": "yes"}))

	require.NoError(t, m.CopyObject(ctx, "dst", "k2", driver.CopySource{Bucket: "src", Key: "k"}))

	info, err := m.HeadObject(ctx, "dst", "k2")
	require.NoError(t, err)
	assert.Equal(t, "yes", info.Metadata["orig"], "copy with no override inherits source metadata")
}

func TestCopyObjectV2ReplaceMetadata(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "src"))
	require.NoError(t, m.CreateBucket(ctx, "dst"))
	require.NoError(t, m.PutObject(ctx, "src", "k", []byte("data"), "text/plain", map[string]string{"orig": "yes"}))

	res, err := m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "dst", DstKey: "k2",
		Src:             driver.CopySource{Bucket: "src", Key: "k"},
		ReplaceMetadata: true,
		Metadata:        map[string]string{"fresh": "new"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.ETag)

	info, err := m.HeadObject(ctx, "dst", "k2")
	require.NoError(t, err)
	assert.Equal(t, "new", info.Metadata["fresh"], "override metadata applied to destination")
	_, inherited := info.Metadata["orig"]
	assert.False(t, inherited, "full replace must drop source metadata")
}

func TestCopyObjectV2InheritWhenNotReplacing(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "src"))
	require.NoError(t, m.CreateBucket(ctx, "dst"))
	require.NoError(t, m.PutObject(ctx, "src", "k", []byte("data"), "text/plain", map[string]string{"orig": "yes"}))

	_, err := m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "dst", DstKey: "k2",
		Src: driver.CopySource{Bucket: "src", Key: "k"},
	})
	require.NoError(t, err)

	info, err := m.HeadObject(ctx, "dst", "k2")
	require.NoError(t, err)
	assert.Equal(t, "yes", info.Metadata["orig"], "no override inherits source metadata")
}
