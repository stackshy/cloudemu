package gcs

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
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts)
}

func TestCreateBucket(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		bucket    string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", bucket: "my-bucket"},
		{name: "empty name", bucket: "", wantErr: true, errSubstr: "empty"},
		{name: "duplicate", bucket: "my-bucket", wantErr: true, errSubstr: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CreateBucket(ctx, tt.bucket)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteBucket(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	tests := []struct {
		name      string
		bucket    string
		setup     func()
		wantErr   bool
		errSubstr string
	}{
		{name: "not found", bucket: "nonexistent", wantErr: true, errSubstr: "not found"},
		{name: "not empty", bucket: "b1", setup: func() {
			require.NoError(t, m.PutObject(ctx, "b1", "key", []byte("data"), "text/plain", nil))
		}, wantErr: true, errSubstr: "not empty"},
		{name: "success after cleanup", bucket: "b1", setup: func() {
			require.NoError(t, m.DeleteObject(ctx, "b1", "key"))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			err := m.DeleteBucket(ctx, tt.bucket)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestListBuckets(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	buckets, err := m.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Empty(t, buckets)

	require.NoError(t, m.CreateBucket(ctx, "beta"))
	require.NoError(t, m.CreateBucket(ctx, "alpha"))

	buckets, err = m.ListBuckets(ctx)
	require.NoError(t, err)
	require.Len(t, buckets, 2)
	assert.Equal(t, "alpha", buckets[0].Name)
	assert.Equal(t, "beta", buckets[1].Name)
	assert.Equal(t, "us-central1", buckets[0].Region)
}

func TestPutAndGetObject(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	tests := []struct {
		name      string
		bucket    string
		key       string
		data      []byte
		wantErr   bool
		errSubstr string
	}{
		{name: "success", bucket: "b1", key: "hello.txt", data: []byte("hello")},
		{name: "bucket not found", bucket: "nope", key: "k", data: []byte("d"), wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.PutObject(ctx, tt.bucket, tt.key, tt.data, "text/plain", map[string]string{"foo": "bar"})
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				obj, getErr := m.GetObject(ctx, tt.bucket, tt.key)
				require.NoError(t, getErr)
				assert.Equal(t, tt.data, obj.Data)
				assert.Equal(t, "text/plain", obj.Info.ContentType)
				assert.Equal(t, "bar", obj.Info.Metadata["foo"])
				assert.Equal(t, int64(len(tt.data)), obj.Info.Size)
			}
		})
	}
}

func TestGetObjectErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	tests := []struct {
		name      string
		bucket    string
		key       string
		errSubstr string
	}{
		{name: "bucket not found", bucket: "nope", key: "k", errSubstr: "not found"},
		{name: "object not found", bucket: "b1", key: "missing", errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.GetObject(ctx, tt.bucket, tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errSubstr)
		})
	}
}

func TestDeleteObject(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	require.NoError(t, m.PutObject(ctx, "b1", "k1", []byte("d"), "", nil))

	tests := []struct {
		name      string
		bucket    string
		key       string
		wantErr   bool
		errSubstr string
	}{
		{name: "bucket not found", bucket: "nope", key: "k", wantErr: true, errSubstr: "not found"},
		{name: "object not found", bucket: "b1", key: "missing", wantErr: true, errSubstr: "not found"},
		{name: "success", bucket: "b1", key: "k1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteObject(ctx, tt.bucket, tt.key)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestListObjects(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	require.NoError(t, m.PutObject(ctx, "b1", "docs/a.txt", []byte("a"), "", nil))
	require.NoError(t, m.PutObject(ctx, "b1", "docs/b.txt", []byte("b"), "", nil))
	require.NoError(t, m.PutObject(ctx, "b1", "images/c.jpg", []byte("c"), "", nil))

	tests := []struct {
		name           string
		bucket         string
		opts           driver.ListOptions
		wantCount      int
		wantPrefixes   int
		wantErr        bool
		wantTruncated  bool
	}{
		{name: "all objects", bucket: "b1", opts: driver.ListOptions{}, wantCount: 3},
		{name: "with prefix", bucket: "b1", opts: driver.ListOptions{Prefix: "docs/"}, wantCount: 2},
		{name: "with delimiter", bucket: "b1", opts: driver.ListOptions{Delimiter: "/"}, wantCount: 0, wantPrefixes: 2},
		{name: "max keys truncation", bucket: "b1", opts: driver.ListOptions{MaxKeys: 1}, wantCount: 1, wantTruncated: true},
		{name: "bucket not found", bucket: "nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.ListObjects(ctx, tt.bucket, tt.opts)
			switch {
			case tt.wantErr:
				require.Error(t, err)
			default:
				require.NoError(t, err)
				assert.Len(t, result.Objects, tt.wantCount)
				assert.Len(t, result.CommonPrefixes, tt.wantPrefixes)
				assert.Equal(t, tt.wantTruncated, result.IsTruncated)
			}
		})
	}
}

func TestCopyObject(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "src"))
	require.NoError(t, m.CreateBucket(ctx, "dst"))
	require.NoError(t, m.PutObject(ctx, "src", "file.txt", []byte("content"), "text/plain", map[string]string{"k": "v"}))

	tests := []struct {
		name      string
		dstBucket string
		dstKey    string
		src       driver.CopySource
		wantErr   bool
		errSubstr string
	}{
		{name: "success", dstBucket: "dst", dstKey: "copy.txt", src: driver.CopySource{Bucket: "src", Key: "file.txt"}},
		{name: "source bucket not found", dstBucket: "dst", dstKey: "x", src: driver.CopySource{Bucket: "nope", Key: "file.txt"}, wantErr: true, errSubstr: "not found"},
		{name: "source object not found", dstBucket: "dst", dstKey: "x", src: driver.CopySource{Bucket: "src", Key: "missing"}, wantErr: true, errSubstr: "not found"},
		{name: "dest bucket not found", dstBucket: "nope", dstKey: "x", src: driver.CopySource{Bucket: "src", Key: "file.txt"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CopyObject(ctx, tt.dstBucket, tt.dstKey, tt.src)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				obj, getErr := m.GetObject(ctx, tt.dstBucket, tt.dstKey)
				require.NoError(t, getErr)
				assert.Equal(t, []byte("content"), obj.Data)
				assert.Equal(t, "v", obj.Info.Metadata["k"])
			}
		})
	}
}
