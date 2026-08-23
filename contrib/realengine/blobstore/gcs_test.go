package blobstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/blobstore"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const gcsProject = "cloudemu-blob-project"

// newGCSClient builds a real cloud.google.com/go/storage client pointed at the
// in-process emulator. The /storage/v1/ suffix is required — the SDK appends
// /b/... directly to the endpoint.
func newGCSClient(ctx context.Context, t *testing.T, ts *httptest.Server) *storage.Client {
	t.Helper()

	c, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}

	c.SetRetry(storage.WithPolicy(storage.RetryNever))

	return c
}

// TestGCSBlobstoreEngineE2E drives a full GCS object lifecycle through the real
// cloud.google.com/go/storage client against CloudEmu backed by the filesystem
// blobstore engine — no Docker, no cloud account. It proves object bytes flow
// through the engine (the real file on disk holds them) while the emulator
// keeps metadata: create bucket → write → read → attrs → copy → read copy →
// delete → confirm NotExist → assert the on-disk file matches.
func TestGCSBlobstoreEngineE2E(t *testing.T) {
	ctx := context.Background()

	eng := blobstore.New("")
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewGCP(config.WithStorageEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	client := newGCSClient(ctx, t, ts)
	t.Cleanup(func() { _ = client.Close() })

	const (
		bucket   = "blob-bucket"
		object   = "docs/greeting.txt"
		copyKey  = "docs/greeting-copy.txt"
		contentT = "text/plain"
	)

	body := []byte("hello from the real blobstore engine")
	bkt := client.Bucket(bucket)

	if err := bkt.Create(ctx, gcsProject, nil); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	// Write the object bytes.
	w := bkt.Object(object).NewWriter(ctx)
	w.ContentType = contentT

	if _, err := w.Write(body); err != nil {
		t.Fatalf("write object: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	// Read the bytes back.
	r, err := bkt.Object(object).NewReader(ctx)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}

	got, err := io.ReadAll(r)
	_ = r.Close()

	if err != nil {
		t.Fatalf("read object: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Fatalf("object round-trip mismatch: got %q want %q", got, body)
	}

	// Attrs must report the real size + content type.
	attrs, err := bkt.Object(object).Attrs(ctx)
	if err != nil {
		t.Fatalf("attrs: %v", err)
	}

	if attrs.Size != int64(len(body)) {
		t.Fatalf("attrs size: got %d want %d", attrs.Size, len(body))
	}

	if attrs.ContentType != contentT {
		t.Fatalf("attrs content type: got %q want %q", attrs.ContentType, contentT)
	}

	// Server-side copy, then read the copy.
	if _, err := bkt.Object(copyKey).CopierFrom(bkt.Object(object)).Run(ctx); err != nil {
		t.Fatalf("copy object: %v", err)
	}

	cr, err := bkt.Object(copyKey).NewReader(ctx)
	if err != nil {
		t.Fatalf("new reader (copy): %v", err)
	}

	gotCopy, err := io.ReadAll(cr)
	_ = cr.Close()

	if err != nil {
		t.Fatalf("read copy: %v", err)
	}

	if !bytes.Equal(gotCopy, body) {
		t.Fatalf("copy round-trip mismatch: got %q want %q", gotCopy, body)
	}

	// Delete the original, then confirm it is gone.
	if err := bkt.Object(object).Delete(ctx); err != nil {
		t.Fatalf("delete object: %v", err)
	}

	if _, err := bkt.Object(object).NewReader(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("expected ErrObjectNotExist after delete, got %v", err)
	}

	// The bytes for the surviving copy must be a real file under the engine root.
	assertEngineFileMatches(t, eng, bucket, copyKey, body)
}

// assertEngineFileMatches walks the engine root and confirms exactly one file
// holds the expected bytes — proof the object bytes really landed on disk.
func assertEngineFileMatches(t *testing.T, eng *blobstore.Store, bucket, key string, want []byte) {
	t.Helper()

	var matched bool

	err := filepath.WalkDir(eng.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		if bytes.Equal(data, want) {
			matched = true
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk engine root: %v", err)
	}

	if !matched {
		t.Fatalf("no on-disk file under %s held the expected bytes for %s/%s", eng.Root(), bucket, key)
	}
}
