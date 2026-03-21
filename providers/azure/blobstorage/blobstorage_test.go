package blobstorage

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
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

func TestGeneratePresignedURL(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "sas-bucket"))

	tests := []struct {
		name    string
		req     driver.PresignedURLRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "GET presigned URL",
			req:  driver.PresignedURLRequest{Bucket: "sas-bucket", Key: "file.txt", Method: "GET", ExpiresIn: time.Hour},
		},
		{
			name: "PUT presigned URL",
			req:  driver.PresignedURLRequest{Bucket: "sas-bucket", Key: "upload.txt", Method: "PUT", ExpiresIn: time.Hour},
		},
		{
			name: "default expiry",
			req:  driver.PresignedURLRequest{Bucket: "sas-bucket", Key: "file.txt", Method: "GET"},
		},
		{
			name:    "container not found",
			req:     driver.PresignedURLRequest{Bucket: "missing", Key: "file.txt", Method: "GET"},
			wantErr: true, errMsg: "not found",
		},
		{
			name:    "invalid method",
			req:     driver.PresignedURLRequest{Bucket: "sas-bucket", Key: "file.txt", Method: "DELETE"},
			wantErr: true, errMsg: "method must be GET or PUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.GeneratePresignedURL(ctx, tt.req)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, result.URL)
				assert.Equal(t, tt.req.Method, result.Method)
				assert.False(t, result.ExpiresAt.IsZero())
				assert.Contains(t, result.URL, "blob.core.windows.net")
			}
		})
	}
}

func TestBucketLifecycleConfig(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("eastus"))
	m := New(opts)

	require.NoError(t, m.CreateBucket(ctx, "lifecycle-bucket"))

	t.Run("put and get lifecycle config", func(t *testing.T) {
		cfg := driver.LifecycleConfig{
			Rules: []driver.LifecycleRule{
				{ID: "expire-logs", Prefix: "logs/", ExpirationDays: 30, Enabled: true},
				{ID: "expire-tmp", Prefix: "tmp/", ExpirationDays: 7, Enabled: true},
			},
		}
		require.NoError(t, m.PutLifecycleConfig(ctx, "lifecycle-bucket", cfg))

		got, err := m.GetLifecycleConfig(ctx, "lifecycle-bucket")
		require.NoError(t, err)
		assert.Len(t, got.Rules, 2)
		assert.Equal(t, "expire-logs", got.Rules[0].ID)
	})

	t.Run("get lifecycle not configured", func(t *testing.T) {
		require.NoError(t, m.CreateBucket(ctx, "no-lifecycle"))
		_, err := m.GetLifecycleConfig(ctx, "no-lifecycle")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no lifecycle")
	})

	t.Run("evaluate lifecycle expires old objects", func(t *testing.T) {
		require.NoError(t, m.PutObject(ctx, "lifecycle-bucket", "logs/old.txt", []byte("old"), "text/plain", nil))
		require.NoError(t, m.PutObject(ctx, "lifecycle-bucket", "keep.txt", []byte("keep"), "text/plain", nil))

		// Advance clock past 30-day expiration
		clk.Advance(31 * 24 * time.Hour)

		expired, err := m.EvaluateLifecycle(ctx, "lifecycle-bucket")
		require.NoError(t, err)
		assert.Contains(t, expired, "logs/old.txt")
	})

	t.Run("container not found", func(t *testing.T) {
		err := m.PutLifecycleConfig(ctx, "missing", driver.LifecycleConfig{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMultipartUpload(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "mp-bucket"))

	t.Run("full multipart upload flow", func(t *testing.T) {
		upload, err := m.CreateMultipartUpload(ctx, "mp-bucket", "large-file.bin", "application/octet-stream")
		require.NoError(t, err)
		assert.NotEmpty(t, upload.UploadID)
		assert.Equal(t, "mp-bucket", upload.Bucket)
		assert.Equal(t, "large-file.bin", upload.Key)

		part1, err := m.UploadPart(ctx, "mp-bucket", "large-file.bin", upload.UploadID, 1, []byte("part1-"))
		require.NoError(t, err)
		assert.Equal(t, 1, part1.PartNumber)
		assert.NotEmpty(t, part1.ETag)

		part2, err := m.UploadPart(ctx, "mp-bucket", "large-file.bin", upload.UploadID, 2, []byte("part2"))
		require.NoError(t, err)
		assert.Equal(t, 2, part2.PartNumber)

		err = m.CompleteMultipartUpload(ctx, "mp-bucket", "large-file.bin", upload.UploadID, []driver.UploadPart{*part1, *part2})
		require.NoError(t, err)

		obj, err := m.GetObject(ctx, "mp-bucket", "large-file.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("part1-part2"), obj.Data)
	})

	t.Run("list multipart uploads", func(t *testing.T) {
		_, err := m.CreateMultipartUpload(ctx, "mp-bucket", "pending.bin", "application/octet-stream")
		require.NoError(t, err)

		uploads, err := m.ListMultipartUploads(ctx, "mp-bucket")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(uploads), 1)
	})

	t.Run("container not found", func(t *testing.T) {
		_, err := m.CreateMultipartUpload(ctx, "missing", "file.bin", "text/plain")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestAbortMultipartUpload(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "abort-bucket"))

	upload, err := m.CreateMultipartUpload(ctx, "abort-bucket", "file.bin", "application/octet-stream")
	require.NoError(t, err)

	tests := []struct {
		name     string
		bucket   string
		uploadID string
		wantErr  bool
		errMsg   string
	}{
		{name: "success", bucket: "abort-bucket", uploadID: upload.UploadID},
		{name: "upload not found", bucket: "abort-bucket", uploadID: "bad-id", wantErr: true, errMsg: "not found"},
		{name: "container not found", bucket: "missing", uploadID: "x", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.AbortMultipartUpload(ctx, tt.bucket, "file.bin", tt.uploadID)

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

func TestBucketVersioning(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "ver-bucket"))

	t.Run("default versioning is disabled", func(t *testing.T) {
		enabled, err := m.GetBucketVersioning(ctx, "ver-bucket")
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("enable versioning", func(t *testing.T) {
		require.NoError(t, m.SetBucketVersioning(ctx, "ver-bucket", true))

		enabled, err := m.GetBucketVersioning(ctx, "ver-bucket")
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("disable versioning", func(t *testing.T) {
		require.NoError(t, m.SetBucketVersioning(ctx, "ver-bucket", false))

		enabled, err := m.GetBucketVersioning(ctx, "ver-bucket")
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("container not found", func(t *testing.T) {
		err := m.SetBucketVersioning(ctx, "missing", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		_, err = m.GetBucketVersioning(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMetricsEmission(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("eastus"))
	m := New(opts)

	mon := &metricsCollector{}
	m.SetMonitoring(mon)

	ctx := context.Background()
	require.NoError(t, m.CreateBucket(ctx, "metrics-bucket"))

	t.Run("PutObject emits metrics", func(t *testing.T) {
		mon.reset()
		require.NoError(t, m.PutObject(ctx, "metrics-bucket", "file.txt", []byte("hello"), "text/plain", nil))
		assert.True(t, mon.hasMetric("Microsoft.Storage/storageAccounts", "Transactions"))
		assert.True(t, mon.hasMetric("Microsoft.Storage/storageAccounts", "Ingress"))
	})

	t.Run("GetObject emits metrics", func(t *testing.T) {
		mon.reset()
		_, err := m.GetObject(ctx, "metrics-bucket", "file.txt")
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.Storage/storageAccounts", "Transactions"))
		assert.True(t, mon.hasMetric("Microsoft.Storage/storageAccounts", "Egress"))
	})

	t.Run("DeleteObject emits metrics", func(t *testing.T) {
		mon.reset()
		require.NoError(t, m.DeleteObject(ctx, "metrics-bucket", "file.txt"))
		assert.True(t, mon.hasMetric("Microsoft.Storage/storageAccounts", "Transactions"))
	})
}

func TestCompleteMultipartUploadPartsValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "mp-val"))

	t.Run("only part 1 yields part 1 data", func(t *testing.T) {
		upload, err := m.CreateMultipartUpload(ctx, "mp-val", "file1.bin", "application/octet-stream")
		require.NoError(t, err)

		part1, err := m.UploadPart(ctx, "mp-val", "file1.bin", upload.UploadID, 1, []byte("AAAA"))
		require.NoError(t, err)
		_, err = m.UploadPart(ctx, "mp-val", "file1.bin", upload.UploadID, 2, []byte("BBBB"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "mp-val", "file1.bin", upload.UploadID, []driver.UploadPart{*part1})
		require.NoError(t, err)

		obj, err := m.GetObject(ctx, "mp-val", "file1.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("AAAA"), obj.Data)
	})

	t.Run("reversed order yields part2+part1 data", func(t *testing.T) {
		upload, err := m.CreateMultipartUpload(ctx, "mp-val", "file2.bin", "application/octet-stream")
		require.NoError(t, err)

		part1, err := m.UploadPart(ctx, "mp-val", "file2.bin", upload.UploadID, 1, []byte("AAAA"))
		require.NoError(t, err)
		part2, err := m.UploadPart(ctx, "mp-val", "file2.bin", upload.UploadID, 2, []byte("BBBB"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "mp-val", "file2.bin", upload.UploadID, []driver.UploadPart{*part2, *part1})
		require.NoError(t, err)

		obj, err := m.GetObject(ctx, "mp-val", "file2.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("BBBBAAAA"), obj.Data)
	})

	t.Run("non-existent part 99 returns error", func(t *testing.T) {
		upload, err := m.CreateMultipartUpload(ctx, "mp-val", "file3.bin", "application/octet-stream")
		require.NoError(t, err)

		_, err = m.UploadPart(ctx, "mp-val", "file3.bin", upload.UploadID, 1, []byte("AAAA"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "mp-val", "file3.bin", upload.UploadID, []driver.UploadPart{
			{PartNumber: 99, ETag: "fake"},
		})
		require.Error(t, err)
	})
}

func TestCopyObjectMetrics(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("eastus"))
	m := New(opts)

	mon := &metricsCollector{}
	m.SetMonitoring(mon)

	require.NoError(t, m.CreateBucket(ctx, "src-bucket"))
	require.NoError(t, m.CreateBucket(ctx, "dst-bucket"))
	require.NoError(t, m.PutObject(ctx, "src-bucket", "file.txt", []byte("data"), "text/plain", nil))

	mon.reset()
	err := m.CopyObject(ctx, "dst-bucket", "copy.txt", driver.CopySource{Bucket: "src-bucket", Key: "file.txt"})
	require.NoError(t, err)
	assert.True(t, mon.hasMetric("Microsoft.Storage/storageAccounts", "Transactions"))
}

// metricsCollector is a simple monitoring stub that records emitted metrics.
type metricsCollector struct {
	data []mondriver.MetricDatum
}

func (c *metricsCollector) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	c.data = append(c.data, data...)
	return nil
}

func (c *metricsCollector) GetMetricData(_ context.Context, _ mondriver.GetMetricInput) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (c *metricsCollector) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (c *metricsCollector) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (c *metricsCollector) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (c *metricsCollector) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *metricsCollector) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *metricsCollector) reset() {
	c.data = nil
}

func (c *metricsCollector) hasMetric(namespace, metricName string) bool {
	for _, d := range c.data {
		if d.Namespace == namespace && d.MetricName == metricName {
			return true
		}
	}
	return false
}
