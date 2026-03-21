package s3

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/storage/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func TestCreateBucket(t *testing.T) {
	tests := []struct {
		name      string
		bucket    string
		setup     func(m *Mock)
		expectErr bool
	}{
		{
			name:   "success",
			bucket: "my-bucket",
		},
		{
			name:      "empty name",
			bucket:    "",
			expectErr: true,
		},
		{
			name:   "already exists",
			bucket: "dup-bucket",
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "dup-bucket")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.CreateBucket(context.Background(), tc.bucket)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestDeleteBucket(t *testing.T) {
	tests := []struct {
		name      string
		bucket    string
		setup     func(m *Mock)
		expectErr bool
	}{
		{
			name:   "success",
			bucket: "my-bucket",
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "my-bucket")
			},
		},
		{
			name:      "not found",
			bucket:    "nonexistent",
			expectErr: true,
		},
		{
			name:   "not empty",
			bucket: "full-bucket",
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "full-bucket")
				_ = m.PutObject(context.Background(), "full-bucket", "key", []byte("data"), "text/plain", nil)
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.DeleteBucket(context.Background(), tc.bucket)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestListBuckets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	buckets, err := m.ListBuckets(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(buckets))

	_ = m.CreateBucket(ctx, "alpha")
	_ = m.CreateBucket(ctx, "beta")

	buckets, err = m.ListBuckets(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(buckets))
	assertEqual(t, "alpha", buckets[0].Name)
	assertEqual(t, "beta", buckets[1].Name)
	assertEqual(t, "us-east-1", buckets[0].Region)
}

func TestPutAndGetObject(t *testing.T) {
	tests := []struct {
		name        string
		bucket      string
		key         string
		data        []byte
		contentType string
		metadata    map[string]string
		setup       func(m *Mock)
		expectErr   bool
	}{
		{
			name:        "success",
			bucket:      "my-bucket",
			key:         "hello.txt",
			data:        []byte("hello world"),
			contentType: "text/plain",
			metadata:    map[string]string{"author": "test"},
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "my-bucket")
			},
		},
		{
			name:      "bucket not found",
			bucket:    "nonexistent",
			key:       "key",
			data:      []byte("data"),
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()
			if tc.setup != nil {
				tc.setup(m)
			}

			err := m.PutObject(ctx, tc.bucket, tc.key, tc.data, tc.contentType, tc.metadata)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			obj, err := m.GetObject(ctx, tc.bucket, tc.key)
			requireNoError(t, err)
			assertEqual(t, string(tc.data), string(obj.Data))
			assertEqual(t, tc.contentType, obj.Info.ContentType)
			assertEqual(t, tc.key, obj.Info.Key)
			assertEqual(t, int64(len(tc.data)), obj.Info.Size)
		})
	}
}

func TestGetObjectNotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.GetObject(ctx, "nonexistent", "key")
	assertError(t, err, true)

	_ = m.CreateBucket(ctx, "bucket")
	_, err = m.GetObject(ctx, "bucket", "missing-key")
	assertError(t, err, true)
}

func TestDeleteObject(t *testing.T) {
	tests := []struct {
		name      string
		bucket    string
		key       string
		setup     func(m *Mock)
		expectErr bool
	}{
		{
			name:   "success",
			bucket: "bkt",
			key:    "obj",
			setup: func(m *Mock) {
				ctx := context.Background()
				_ = m.CreateBucket(ctx, "bkt")
				_ = m.PutObject(ctx, "bkt", "obj", []byte("data"), "", nil)
			},
		},
		{
			name:      "bucket not found",
			bucket:    "nope",
			key:       "key",
			expectErr: true,
		},
		{
			name:   "object not found",
			bucket: "bkt",
			key:    "missing",
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "bkt")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.DeleteObject(context.Background(), tc.bucket, tc.key)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestListObjects(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")
	_ = m.PutObject(ctx, "bkt", "dir/a.txt", []byte("a"), "", nil)
	_ = m.PutObject(ctx, "bkt", "dir/b.txt", []byte("b"), "", nil)
	_ = m.PutObject(ctx, "bkt", "other.txt", []byte("c"), "", nil)

	t.Run("list all", func(t *testing.T) {
		result, err := m.ListObjects(ctx, "bkt", driver.ListOptions{})
		requireNoError(t, err)
		assertEqual(t, 3, len(result.Objects))
	})

	t.Run("with prefix", func(t *testing.T) {
		result, err := m.ListObjects(ctx, "bkt", driver.ListOptions{Prefix: "dir/"})
		requireNoError(t, err)
		assertEqual(t, 2, len(result.Objects))
	})

	t.Run("with delimiter", func(t *testing.T) {
		result, err := m.ListObjects(ctx, "bkt", driver.ListOptions{Delimiter: "/"})
		requireNoError(t, err)
		assertEqual(t, 1, len(result.Objects))
		assertEqual(t, 1, len(result.CommonPrefixes))
		assertEqual(t, "dir/", result.CommonPrefixes[0])
	})

	t.Run("bucket not found", func(t *testing.T) {
		_, err := m.ListObjects(ctx, "nope", driver.ListOptions{})
		assertError(t, err, true)
	})
}

func TestCopyObject(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(m *Mock)
		dst       string
		dstKey    string
		src       driver.CopySource
		expectErr bool
	}{
		{
			name: "success",
			setup: func(m *Mock) {
				ctx := context.Background()
				_ = m.CreateBucket(ctx, "src-bkt")
				_ = m.CreateBucket(ctx, "dst-bkt")
				_ = m.PutObject(ctx, "src-bkt", "file.txt", []byte("data"), "text/plain", map[string]string{"k": "v"})
			},
			dst:    "dst-bkt",
			dstKey: "copy.txt",
			src:    driver.CopySource{Bucket: "src-bkt", Key: "file.txt"},
		},
		{
			name:      "source bucket not found",
			dst:       "dst",
			dstKey:    "key",
			src:       driver.CopySource{Bucket: "nope", Key: "key"},
			expectErr: true,
		},
		{
			name: "source object not found",
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "src-bkt2")
			},
			dst:       "dst",
			dstKey:    "key",
			src:       driver.CopySource{Bucket: "src-bkt2", Key: "nope"},
			expectErr: true,
		},
		{
			name: "destination bucket not found",
			setup: func(m *Mock) {
				ctx := context.Background()
				_ = m.CreateBucket(ctx, "src-bkt3")
				_ = m.PutObject(ctx, "src-bkt3", "f.txt", []byte("d"), "", nil)
			},
			dst:       "nope",
			dstKey:    "key",
			src:       driver.CopySource{Bucket: "src-bkt3", Key: "f.txt"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.CopyObject(context.Background(), tc.dst, tc.dstKey, tc.src)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			obj, err := m.GetObject(context.Background(), tc.dst, tc.dstKey)
			requireNoError(t, err)
			assertEqual(t, "data", string(obj.Data))
		})
	}
}

func TestHeadObject(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")
	_ = m.PutObject(ctx, "bkt", "file.txt", []byte("hello"), "text/plain", nil)

	info, err := m.HeadObject(ctx, "bkt", "file.txt")
	requireNoError(t, err)
	assertEqual(t, "file.txt", info.Key)
	assertEqual(t, int64(5), info.Size)

	_, err = m.HeadObject(ctx, "bkt", "missing")
	assertError(t, err, true)

	_, err = m.HeadObject(ctx, "nope", "file.txt")
	assertError(t, err, true)
}

func TestGeneratePresignedURL(t *testing.T) {
	tests := []struct {
		name      string
		req       driver.PresignedURLRequest
		setup     func(m *Mock)
		expectErr bool
		checkURL  func(t *testing.T, url *driver.PresignedURL)
	}{
		{
			name: "GET presigned URL",
			req:  driver.PresignedURLRequest{Bucket: "bkt", Key: "file.txt", Method: http.MethodGet},
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "bkt")
			},
			checkURL: func(t *testing.T, url *driver.PresignedURL) {
				t.Helper()
				assertEqual(t, http.MethodGet, url.Method)
				assertEqual(t, true, strings.Contains(url.URL, "bkt"))
				assertEqual(t, true, strings.Contains(url.URL, "file.txt"))
				assertEqual(t, true, strings.Contains(url.URL, "X-Amz-Signature"))
				assertEqual(t, true, strings.Contains(url.URL, "X-Amz-Expires"))
			},
		},
		{
			name: "PUT presigned URL",
			req:  driver.PresignedURLRequest{Bucket: "bkt", Key: "upload.bin", Method: http.MethodPut},
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "bkt")
			},
			checkURL: func(t *testing.T, url *driver.PresignedURL) {
				t.Helper()
				assertEqual(t, http.MethodPut, url.Method)
				assertEqual(t, true, strings.Contains(url.URL, "upload.bin"))
			},
		},
		{
			name: "custom expiry",
			req:  driver.PresignedURLRequest{Bucket: "bkt", Key: "k", Method: http.MethodGet, ExpiresIn: 30 * time.Minute},
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "bkt")
			},
			checkURL: func(t *testing.T, url *driver.PresignedURL) {
				t.Helper()
				assertEqual(t, true, strings.Contains(url.URL, "1800"))
			},
		},
		{
			name:      "bucket not found",
			req:       driver.PresignedURLRequest{Bucket: "nope", Key: "k", Method: http.MethodGet},
			expectErr: true,
		},
		{
			name: "invalid method",
			req:  driver.PresignedURLRequest{Bucket: "bkt", Key: "k", Method: http.MethodPost},
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "bkt")
			},
			expectErr: true,
		},
		{
			name: "expiry exceeds maximum",
			req:  driver.PresignedURLRequest{Bucket: "bkt", Key: "k", Method: http.MethodGet, ExpiresIn: 8 * 24 * time.Hour},
			setup: func(m *Mock) {
				_ = m.CreateBucket(context.Background(), "bkt")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			url, err := m.GeneratePresignedURL(context.Background(), tc.req)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			tc.checkURL(t, url)
		})
	}
}

func TestBucketLifecycleConfig(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")

	t.Run("put and get lifecycle config", func(t *testing.T) {
		cfg := driver.LifecycleConfig{
			Rules: []driver.LifecycleRule{
				{ID: "expire-logs", Enabled: true, Prefix: "logs/", ExpirationDays: 30},
				{ID: "disabled-rule", Enabled: false, Prefix: "tmp/", ExpirationDays: 7},
			},
		}

		err := m.PutLifecycleConfig(ctx, "bkt", cfg)
		requireNoError(t, err)

		got, err := m.GetLifecycleConfig(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, 2, len(got.Rules))
		assertEqual(t, "expire-logs", got.Rules[0].ID)
		assertEqual(t, 30, got.Rules[0].ExpirationDays)
	})

	t.Run("get lifecycle no config", func(t *testing.T) {
		_ = m.CreateBucket(ctx, "empty-bkt")
		_, err := m.GetLifecycleConfig(ctx, "empty-bkt")
		assertError(t, err, true)
	})

	t.Run("bucket not found", func(t *testing.T) {
		err := m.PutLifecycleConfig(ctx, "nope", driver.LifecycleConfig{})
		assertError(t, err, true)
	})

	t.Run("evaluate lifecycle with FakeClock", func(t *testing.T) {
		_ = m.PutObject(ctx, "bkt", "logs/old.txt", []byte("old"), "", nil)
		_ = m.PutObject(ctx, "bkt", "logs/new.txt", []byte("new"), "", nil)
		_ = m.PutObject(ctx, "bkt", "data/keep.txt", []byte("keep"), "", nil)

		// Before expiry: no expired keys
		expired, err := m.EvaluateLifecycle(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, 0, len(expired))

		// Advance clock past 30 days
		fc.Advance(31 * 24 * time.Hour)

		expired, err = m.EvaluateLifecycle(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, 2, len(expired))
		assertEqual(t, "logs/new.txt", expired[0])
		assertEqual(t, "logs/old.txt", expired[1])
	})
}

func TestMultipartUpload(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")

	t.Run("create upload parts and complete", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "bkt", "big-file.bin", "application/octet-stream")
		requireNoError(t, err)
		assertEqual(t, "bkt", mp.Bucket)
		assertEqual(t, "big-file.bin", mp.Key)

		part1, err := m.UploadPart(ctx, "bkt", "big-file.bin", mp.UploadID, 1, []byte("part1-"))
		requireNoError(t, err)
		assertEqual(t, 1, part1.PartNumber)

		part2, err := m.UploadPart(ctx, "bkt", "big-file.bin", mp.UploadID, 2, []byte("part2"))
		requireNoError(t, err)
		assertEqual(t, 2, part2.PartNumber)

		err = m.CompleteMultipartUpload(ctx, "bkt", "big-file.bin", mp.UploadID, []driver.UploadPart{*part1, *part2})
		requireNoError(t, err)

		obj, err := m.GetObject(ctx, "bkt", "big-file.bin")
		requireNoError(t, err)
		assertEqual(t, "part1-part2", string(obj.Data))
	})

	t.Run("bucket not found for create", func(t *testing.T) {
		_, err := m.CreateMultipartUpload(ctx, "nope", "k", "")
		assertError(t, err, true)
	})

	t.Run("upload not found for upload part", func(t *testing.T) {
		_, err := m.UploadPart(ctx, "bkt", "k", "bad-upload-id", 1, []byte("x"))
		assertError(t, err, true)
	})
}

func TestAbortMultipartUpload(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")

	mp, err := m.CreateMultipartUpload(ctx, "bkt", "file.bin", "")
	requireNoError(t, err)

	_, err = m.UploadPart(ctx, "bkt", "file.bin", mp.UploadID, 1, []byte("data"))
	requireNoError(t, err)

	err = m.AbortMultipartUpload(ctx, "bkt", "file.bin", mp.UploadID)
	requireNoError(t, err)

	// Verify upload is gone
	uploads, err := m.ListMultipartUploads(ctx, "bkt")
	requireNoError(t, err)
	assertEqual(t, 0, len(uploads))

	// Object should not exist
	_, err = m.GetObject(ctx, "bkt", "file.bin")
	assertError(t, err, true)
}

func TestListMultipartUploads(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")

	t.Run("empty list", func(t *testing.T) {
		uploads, err := m.ListMultipartUploads(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, 0, len(uploads))
	})

	t.Run("multiple uploads", func(t *testing.T) {
		_, err := m.CreateMultipartUpload(ctx, "bkt", "file1.bin", "")
		requireNoError(t, err)
		_, err = m.CreateMultipartUpload(ctx, "bkt", "file2.bin", "")
		requireNoError(t, err)

		uploads, err := m.ListMultipartUploads(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, 2, len(uploads))
	})

	t.Run("bucket not found", func(t *testing.T) {
		_, err := m.ListMultipartUploads(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestBucketVersioning(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")

	t.Run("default disabled", func(t *testing.T) {
		enabled, err := m.GetBucketVersioning(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, false, enabled)
	})

	t.Run("enable versioning", func(t *testing.T) {
		err := m.SetBucketVersioning(ctx, "bkt", true)
		requireNoError(t, err)

		enabled, err := m.GetBucketVersioning(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, true, enabled)
	})

	t.Run("disable versioning", func(t *testing.T) {
		err := m.SetBucketVersioning(ctx, "bkt", false)
		requireNoError(t, err)

		enabled, err := m.GetBucketVersioning(ctx, "bkt")
		requireNoError(t, err)
		assertEqual(t, false, enabled)
	})

	t.Run("bucket not found", func(t *testing.T) {
		err := m.SetBucketVersioning(ctx, "nope", true)
		assertError(t, err, true)

		_, err = m.GetBucketVersioning(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestS3MetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	ctx := context.Background()

	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)
	_ = m.CreateBucket(ctx, "bkt")

	t.Run("PutObject emits metrics", func(t *testing.T) {
		err := m.PutObject(ctx, "bkt", "key", []byte("hello"), "text/plain", nil)
		requireNoError(t, err)

		result, err := cw.GetMetricData(ctx, monitoringGetInput("PutRequests", "bkt", fc))
		requireNoError(t, err)
		assertEqual(t, true, len(result.Values) > 0)
	})

	t.Run("GetObject emits metrics", func(t *testing.T) {
		_, err := m.GetObject(ctx, "bkt", "key")
		requireNoError(t, err)

		result, err := cw.GetMetricData(ctx, monitoringGetInput("GetRequests", "bkt", fc))
		requireNoError(t, err)
		assertEqual(t, true, len(result.Values) > 0)
	})
}

func monitoringGetInput(metricName, bucket string, fc *config.FakeClock) mondriver.GetMetricInput {
	return mondriver.GetMetricInput{
		Namespace:  "AWS/S3",
		MetricName: metricName,
		Dimensions: map[string]string{"BucketName": bucket},
		StartTime:  fc.Now().Add(-1 * time.Hour),
		EndTime:    fc.Now().Add(1 * time.Hour),
		Period:     60,
		Stat:       "Sum",
	}
}

func TestCompleteMultipartUploadPartsValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateBucket(ctx, "bkt")

	t.Run("only part 1 yields part 1 data", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "bkt", "file1.bin", "application/octet-stream")
		requireNoError(t, err)

		part1, err := m.UploadPart(ctx, "bkt", "file1.bin", mp.UploadID, 1, []byte("AAAA"))
		requireNoError(t, err)
		_, err = m.UploadPart(ctx, "bkt", "file1.bin", mp.UploadID, 2, []byte("BBBB"))
		requireNoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "bkt", "file1.bin", mp.UploadID, []driver.UploadPart{*part1})
		requireNoError(t, err)

		obj, err := m.GetObject(ctx, "bkt", "file1.bin")
		requireNoError(t, err)
		assertEqual(t, "AAAA", string(obj.Data))
	})

	t.Run("reversed order yields part2+part1 data", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "bkt", "file2.bin", "application/octet-stream")
		requireNoError(t, err)

		part1, err := m.UploadPart(ctx, "bkt", "file2.bin", mp.UploadID, 1, []byte("AAAA"))
		requireNoError(t, err)
		part2, err := m.UploadPart(ctx, "bkt", "file2.bin", mp.UploadID, 2, []byte("BBBB"))
		requireNoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "bkt", "file2.bin", mp.UploadID, []driver.UploadPart{*part2, *part1})
		requireNoError(t, err)

		obj, err := m.GetObject(ctx, "bkt", "file2.bin")
		requireNoError(t, err)
		assertEqual(t, "BBBBAAAA", string(obj.Data))
	})

	t.Run("non-existent part 99 returns error", func(t *testing.T) {
		mp, err := m.CreateMultipartUpload(ctx, "bkt", "file3.bin", "application/octet-stream")
		requireNoError(t, err)

		_, err = m.UploadPart(ctx, "bkt", "file3.bin", mp.UploadID, 1, []byte("AAAA"))
		requireNoError(t, err)

		err = m.CompleteMultipartUpload(ctx, "bkt", "file3.bin", mp.UploadID, []driver.UploadPart{
			{PartNumber: 99, ETag: "fake"},
		})
		assertError(t, err, true)
	})
}

func TestCopyObjectMetrics(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	ctx := context.Background()

	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	_ = m.CreateBucket(ctx, "src-bkt")
	_ = m.CreateBucket(ctx, "dst-bkt")
	_ = m.PutObject(ctx, "src-bkt", "file.txt", []byte("data"), "text/plain", nil)

	err := m.CopyObject(ctx, "dst-bkt", "copy.txt", driver.CopySource{Bucket: "src-bkt", Key: "file.txt"})
	requireNoError(t, err)

	result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "AWS/S3",
		MetricName: "AllRequests",
		Dimensions: map[string]string{"BucketName": "dst-bkt"},
		StartTime:  fc.Now().Add(-1 * time.Hour),
		EndTime:    fc.Now().Add(1 * time.Hour),
		Period:     60,
		Stat:       "Sum",
	})
	requireNoError(t, err)
	assertEqual(t, true, len(result.Values) > 0)
}

// --- test helpers (no if/else, use t.Fatal/t.Errorf) ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}
