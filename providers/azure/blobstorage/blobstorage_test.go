package blobstorage

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("eastus"))

	return New(opts)
}

func TestCreateContainer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		container string
		wantErr   bool
		errMsg    string
	}{
		{name: "success", container: "my-container"},
		{name: "empty name", container: "", wantErr: true, errMsg: "container name cannot be empty"},
		{name: "duplicate", container: "my-container", wantErr: true, errMsg: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CreateBucket(ctx, tt.container)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteContainer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "test-container"))

	tests := []struct {
		name      string
		container string
		setup     func()
		wantErr   bool
		errMsg    string
	}{
		{name: "not found", container: "nonexistent", wantErr: true, errMsg: "not found"},
		{name: "not empty", container: "test-container", setup: func() {
			require.NoError(t, m.PutObject(ctx, "test-container", "key1", []byte("data"), "text/plain", nil))
		}, wantErr: true, errMsg: "not empty"},
		{name: "success after emptying", container: "test-container", setup: func() {
			require.NoError(t, m.DeleteObject(ctx, "test-container", "key1"))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			err := m.DeleteBucket(ctx, tt.container)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestListContainers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty list", func(t *testing.T) {
		buckets, err := m.ListBuckets(ctx)
		require.NoError(t, err)
		assert.Empty(t, buckets)
	})

	t.Run("multiple containers sorted", func(t *testing.T) {
		require.NoError(t, m.CreateBucket(ctx, "beta"))
		require.NoError(t, m.CreateBucket(ctx, "alpha"))

		buckets, err := m.ListBuckets(ctx)
		require.NoError(t, err)
		require.Len(t, buckets, 2)
		assert.Equal(t, "alpha", buckets[0].Name)
		assert.Equal(t, "beta", buckets[1].Name)
		assert.Equal(t, "eastus", buckets[0].Region)
	})
}

func TestPutAndGetBlob(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "bucket"))

	tests := []struct {
		name    string
		bucket  string
		key     string
		data    []byte
		ct      string
		meta    map[string]string
		wantErr bool
		errMsg  string
	}{
		{name: "success", bucket: "bucket", key: "file.txt", data: []byte("hello"), ct: "text/plain", meta: map[string]string{"env": "test"}},
		{name: "container not found", bucket: "missing", key: "file.txt", data: []byte("hello"), ct: "text/plain", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.PutObject(ctx, tt.bucket, tt.key, tt.data, tt.ct, tt.meta)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				obj, err := m.GetObject(ctx, tt.bucket, tt.key)
				require.NoError(t, err)
				assert.Equal(t, tt.data, obj.Data)
				assert.Equal(t, tt.ct, obj.Info.ContentType)
				assert.Equal(t, tt.key, obj.Info.Key)
				assert.Equal(t, int64(len(tt.data)), obj.Info.Size)
				assert.NotEmpty(t, obj.Info.ETag)
			}
		})
	}
}

func TestGetBlobErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "bucket"))

	tests := []struct {
		name   string
		bucket string
		key    string
		errMsg string
	}{
		{name: "container not found", bucket: "missing", key: "key", errMsg: "container"},
		{name: "blob not found", bucket: "bucket", key: "missing", errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.GetObject(ctx, tt.bucket, tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestDeleteBlob(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "bucket"))
	require.NoError(t, m.PutObject(ctx, "bucket", "key1", []byte("data"), "text/plain", nil))

	tests := []struct {
		name    string
		bucket  string
		key     string
		wantErr bool
		errMsg  string
	}{
		{name: "container not found", bucket: "missing", key: "key1", wantErr: true, errMsg: "not found"},
		{name: "blob not found", bucket: "bucket", key: "missing", wantErr: true, errMsg: "not found"},
		{name: "success", bucket: "bucket", key: "key1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteObject(ctx, tt.bucket, tt.key)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestListBlobs(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "bucket"))
	require.NoError(t, m.PutObject(ctx, "bucket", "dir/a.txt", []byte("a"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "bucket", "dir/b.txt", []byte("b"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "bucket", "root.txt", []byte("r"), "text/plain", nil))

	tests := []struct {
		name           string
		bucket         string
		opts           driver.ListOptions
		wantErr        bool
		wantCount      int
		wantPrefixes   []string
		errMsg         string
	}{
		{name: "all objects", bucket: "bucket", opts: driver.ListOptions{}, wantCount: 3},
		{name: "prefix filter", bucket: "bucket", opts: driver.ListOptions{Prefix: "dir/"}, wantCount: 2},
		{name: "delimiter", bucket: "bucket", opts: driver.ListOptions{Delimiter: "/"}, wantCount: 1, wantPrefixes: []string{"dir/"}},
		{name: "container not found", bucket: "missing", opts: driver.ListOptions{}, wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.ListObjects(ctx, tt.bucket, tt.opts)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Len(t, result.Objects, tt.wantCount)

				if tt.wantPrefixes != nil {
					assert.Equal(t, tt.wantPrefixes, result.CommonPrefixes)
				}
			}
		})
	}
}

func TestCopyBlob(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "src-bucket"))
	require.NoError(t, m.CreateBucket(ctx, "dst-bucket"))
	require.NoError(t, m.PutObject(ctx, "src-bucket", "original.txt", []byte("copy me"), "text/plain", map[string]string{"k": "v"}))

	tests := []struct {
		name      string
		dstBucket string
		dstKey    string
		src       driver.CopySource
		wantErr   bool
		errMsg    string
	}{
		{name: "success", dstBucket: "dst-bucket", dstKey: "copied.txt", src: driver.CopySource{Bucket: "src-bucket", Key: "original.txt"}},
		{name: "source container not found", dstBucket: "dst-bucket", dstKey: "x", src: driver.CopySource{Bucket: "missing", Key: "x"}, wantErr: true, errMsg: "source container"},
		{name: "source blob not found", dstBucket: "dst-bucket", dstKey: "x", src: driver.CopySource{Bucket: "src-bucket", Key: "missing"}, wantErr: true, errMsg: "source blob"},
		{name: "dest container not found", dstBucket: "missing", dstKey: "x", src: driver.CopySource{Bucket: "src-bucket", Key: "original.txt"}, wantErr: true, errMsg: "destination container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CopyObject(ctx, tt.dstBucket, tt.dstKey, tt.src)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				obj, err := m.GetObject(ctx, tt.dstBucket, tt.dstKey)
				require.NoError(t, err)
				assert.Equal(t, []byte("copy me"), obj.Data)
				assert.Equal(t, "text/plain", obj.Info.ContentType)
			}
		})
	}
}

func TestHeadObject(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "bucket"))
	require.NoError(t, m.PutObject(ctx, "bucket", "file.txt", []byte("hello"), "text/plain", map[string]string{"k": "v"}))

	t.Run("success", func(t *testing.T) {
		info, err := m.HeadObject(ctx, "bucket", "file.txt")
		require.NoError(t, err)
		assert.Equal(t, int64(5), info.Size)
		assert.Equal(t, "text/plain", info.ContentType)
		assert.Equal(t, "v", info.Metadata["k"])
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.HeadObject(ctx, "bucket", "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}
