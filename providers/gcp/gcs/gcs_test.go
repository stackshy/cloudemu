package gcs

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

func TestGeneratePresignedURL(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	tests := []struct {
		name      string
		req       driver.PresignedURLRequest
		wantErr   bool
		errSubstr string
	}{
		{
			name: "GET success",
			req:  driver.PresignedURLRequest{Bucket: "b1", Key: "obj.txt", Method: "GET"},
		},
		{
			name: "PUT success",
			req:  driver.PresignedURLRequest{Bucket: "b1", Key: "obj.txt", Method: "PUT", ExpiresIn: 10 * time.Minute},
		},
		{
			name:      "invalid method",
			req:       driver.PresignedURLRequest{Bucket: "b1", Key: "obj.txt", Method: "DELETE"},
			wantErr:   true,
			errSubstr: "GET or PUT",
		},
		{
			name:      "bucket not found",
			req:       driver.PresignedURLRequest{Bucket: "nope", Key: "obj.txt", Method: "GET"},
			wantErr:   true,
			errSubstr: "not found",
		},
		{
			name:      "expiry too long",
			req:       driver.PresignedURLRequest{Bucket: "b1", Key: "obj.txt", Method: "GET", ExpiresIn: 8 * 24 * time.Hour},
			wantErr:   true,
			errSubstr: "exceeds maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.GeneratePresignedURL(ctx, tt.req)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, result.URL)
				assert.Contains(t, result.URL, "storage.googleapis.com")
				assert.Equal(t, tt.req.Method, result.Method)
				assert.False(t, result.ExpiresAt.IsZero())
			}
		})
	}
}

func TestBucketLifecycleConfig(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	t.Run("no lifecycle config returns not found", func(t *testing.T) {
		_, err := m.GetLifecycleConfig(ctx, "b1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no lifecycle")
	})

	t.Run("put and get lifecycle config", func(t *testing.T) {
		cfg := driver.LifecycleConfig{
			Rules: []driver.LifecycleRule{
				{ID: "expire-logs", Enabled: true, Prefix: "logs/", ExpirationDays: 30},
			},
		}
		require.NoError(t, m.PutLifecycleConfig(ctx, "b1", cfg))

		got, err := m.GetLifecycleConfig(ctx, "b1")
		require.NoError(t, err)
		require.Len(t, got.Rules, 1)
		assert.Equal(t, "expire-logs", got.Rules[0].ID)
		assert.Equal(t, 30, got.Rules[0].ExpirationDays)
	})

	t.Run("evaluate lifecycle deletes expired objects", func(t *testing.T) {
		require.NoError(t, m.PutObject(ctx, "b1", "logs/old.txt", []byte("old"), "", nil))

		// Advance clock past expiration
		clk := m.opts.Clock.(*config.FakeClock)
		clk.Advance(31 * 24 * time.Hour)

		expired, err := m.EvaluateLifecycle(ctx, "b1")
		require.NoError(t, err)
		assert.Contains(t, expired, "logs/old.txt")
	})

	t.Run("bucket not found", func(t *testing.T) {
		err := m.PutLifecycleConfig(ctx, "missing", driver.LifecycleConfig{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMultipartUpload(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	t.Run("full multipart upload flow", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "b1", "big-file.bin", "application/octet-stream")
		require.NoError(t, err)
		assert.NotEmpty(t, mp.UploadID)
		assert.Equal(t, "b1", mp.Bucket)
		assert.Equal(t, "big-file.bin", mp.Key)

		part1, err := m.UploadPart(ctx, "b1", "big-file.bin", mp.UploadID, 1, []byte("part1-"))
		require.NoError(t, err)
		assert.Equal(t, 1, part1.PartNumber)
		assert.NotEmpty(t, part1.ETag)

		part2, err := m.UploadPart(ctx, "b1", "big-file.bin", mp.UploadID, 2, []byte("part2"))
		require.NoError(t, err)
		assert.Equal(t, 2, part2.PartNumber)

		err = m.CompleteMultipartUpload(ctx, "b1", "big-file.bin", mp.UploadID,
			[]driver.UploadPart{*part1, *part2})
		require.NoError(t, err)

		obj, err := m.GetObject(ctx, "b1", "big-file.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("part1-part2"), obj.Data)
	})

	t.Run("list multipart uploads", func(t *testing.T) {
		_, err := m.CreateMultipartUpload(ctx, "b1", "pending.bin", "")
		require.NoError(t, err)

		uploads, err := m.ListMultipartUploads(ctx, "b1")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(uploads), 1)
	})

	t.Run("bucket not found", func(t *testing.T) {
		_, err := m.CreateMultipartUpload(ctx, "missing", "x", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestAbortMultipartUpload(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	mp, err := m.CreateMultipartUpload(ctx, "b1", "abort-me.bin", "")
	require.NoError(t, err)

	tests := []struct {
		name      string
		bucket    string
		uploadID  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", bucket: "b1", uploadID: mp.UploadID},
		{name: "upload not found after abort", bucket: "b1", uploadID: mp.UploadID, wantErr: true, errSubstr: "not found"},
		{name: "bucket not found", bucket: "missing", uploadID: "x", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.AbortMultipartUpload(ctx, tt.bucket, "", tt.uploadID)
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

func TestBucketVersioning(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	t.Run("default versioning is disabled", func(t *testing.T) {
		enabled, err := m.GetBucketVersioning(ctx, "b1")
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("enable versioning", func(t *testing.T) {
		require.NoError(t, m.SetBucketVersioning(ctx, "b1", true))
		enabled, err := m.GetBucketVersioning(ctx, "b1")
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("disable versioning", func(t *testing.T) {
		require.NoError(t, m.SetBucketVersioning(ctx, "b1", false))
		enabled, err := m.GetBucketVersioning(ctx, "b1")
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("bucket not found", func(t *testing.T) {
		err := m.SetBucketVersioning(ctx, "missing", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		_, err = m.GetBucketVersioning(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	monOpts := config.NewOptions(config.WithClock(clk))
	mon := newMonitoringMock(monOpts)

	m := New(opts)
	m.SetMonitoring(mon)

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	t.Run("PutObject emits request and received bytes metrics", func(t *testing.T) {
		require.NoError(t, m.PutObject(ctx, "b1", "k1", []byte("hello"), "text/plain", nil))

		result, err := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "storage.googleapis.com",
			MetricName: "api/request_count",
			Dimensions: map[string]string{"bucket_name": "b1"},
			StartTime:  clk.Now().Add(-time.Minute),
			EndTime:    clk.Now().Add(time.Minute),
			Period:     60,
			Stat:       "Sum",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Values)
	})

	t.Run("GetObject emits request and sent bytes metrics", func(t *testing.T) {
		_, err := m.GetObject(ctx, "b1", "k1")
		require.NoError(t, err)

		result, err := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "storage.googleapis.com",
			MetricName: "network/sent_bytes_count",
			Dimensions: map[string]string{"bucket_name": "b1"},
			StartTime:  clk.Now().Add(-time.Minute),
			EndTime:    clk.Now().Add(time.Minute),
			Period:     60,
			Stat:       "Sum",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Values)
	})
}

func TestCompleteMultipartUploadPartsValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "mp-val"))

	t.Run("only part 1 yields part 1 data", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "mp-val", "file1.bin", "application/octet-stream")
		require.NoError(t, err)

		part1, err := m.UploadPart(ctx, "mp-val", "file1.bin", mp.UploadID, 1, []byte("AAAA"))
		require.NoError(t, err)
		_, err = m.UploadPart(ctx, "mp-val", "file1.bin", mp.UploadID, 2, []byte("BBBB"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "mp-val", "file1.bin", mp.UploadID, []driver.UploadPart{*part1})
		require.NoError(t, err)

		obj, err := m.GetObject(ctx, "mp-val", "file1.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("AAAA"), obj.Data)
	})

	t.Run("reversed order yields part2+part1 data", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "mp-val", "file2.bin", "application/octet-stream")
		require.NoError(t, err)

		part1, err := m.UploadPart(ctx, "mp-val", "file2.bin", mp.UploadID, 1, []byte("AAAA"))
		require.NoError(t, err)
		part2, err := m.UploadPart(ctx, "mp-val", "file2.bin", mp.UploadID, 2, []byte("BBBB"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "mp-val", "file2.bin", mp.UploadID, []driver.UploadPart{*part2, *part1})
		require.NoError(t, err)

		obj, err := m.GetObject(ctx, "mp-val", "file2.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("BBBBAAAA"), obj.Data)
	})

	t.Run("non-existent part 99 returns error", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "mp-val", "file3.bin", "application/octet-stream")
		require.NoError(t, err)

		_, err = m.UploadPart(ctx, "mp-val", "file3.bin", mp.UploadID, 1, []byte("AAAA"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "mp-val", "file3.bin", mp.UploadID, []driver.UploadPart{
			{PartNumber: 99, ETag: "fake"},
		})
		require.Error(t, err)
	})
}

func TestCopyObjectMetrics(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	monOpts := config.NewOptions(config.WithClock(clk))
	mon := newMonitoringMock(monOpts)

	m := New(opts)
	m.SetMonitoring(mon)

	require.NoError(t, m.CreateBucket(ctx, "src"))
	require.NoError(t, m.CreateBucket(ctx, "dst"))
	require.NoError(t, m.PutObject(ctx, "src", "file.txt", []byte("data"), "text/plain", nil))

	err := m.CopyObject(ctx, "dst", "copy.txt", driver.CopySource{Bucket: "src", Key: "file.txt"})
	require.NoError(t, err)

	result, getErr := mon.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "storage.googleapis.com",
		MetricName: "api/request_count",
		Dimensions: map[string]string{"bucket_name": "dst"},
		StartTime:  clk.Now().Add(-time.Minute),
		EndTime:    clk.Now().Add(time.Minute),
		Period:     60,
		Stat:       "Sum",
	})
	require.NoError(t, getErr)
	assert.NotEmpty(t, result.Values)
}

// newMonitoringMock creates a Cloud Monitoring mock for testing metric emission.
func newMonitoringMock(opts *config.Options) *monitoringMock {
	return &monitoringMock{
		data: make(map[string][]mondriver.MetricDatum),
		opts: opts,
	}
}

// monitoringMock is a minimal in-memory monitoring implementation for testing.
type monitoringMock struct {
	data map[string][]mondriver.MetricDatum
	opts *config.Options
}

func (m *monitoringMock) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	for _, d := range data {
		key := d.Namespace + "/" + d.MetricName
		m.data[key] = append(m.data[key], d)
	}

	return nil
}

func (m *monitoringMock) GetMetricData(
	_ context.Context, input mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	key := input.Namespace + "/" + input.MetricName
	datums := m.data[key]

	var timestamps []time.Time

	var values []float64

	for _, d := range datums {
		if !d.Timestamp.Before(input.StartTime) && !d.Timestamp.After(input.EndTime) {
			timestamps = append(timestamps, d.Timestamp)
			values = append(values, d.Value)
		}
	}

	return &mondriver.MetricDataResult{Timestamps: timestamps, Values: values}, nil
}

func (m *monitoringMock) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (m *monitoringMock) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (m *monitoringMock) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (m *monitoringMock) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (m *monitoringMock) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}
