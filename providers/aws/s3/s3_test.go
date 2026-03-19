package s3

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
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
