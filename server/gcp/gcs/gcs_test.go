package gcs_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

// TestSDKGCSRoundTrip drives Google Cloud Storage operations through the real
// cloud.google.com/go/storage client.
func TestSDKGCSRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	// The Go GCS SDK uses WithEndpoint as the API root and appends /b/...
	// directly (no /storage/v1/ prefix). Pass our path-prefixed endpoint so
	// requests land on the handler's recognised paths.
	client, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	bucket := client.Bucket("b1")

	// Create bucket.
	if err := bucket.Create(ctx, "p1", nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	// Upload object via Writer (multipart upload).
	const objContent = "hello, gcs"

	w := bucket.Object("k1").NewWriter(ctx)
	w.ContentType = "text/plain"
	w.Metadata = map[string]string{"author": "cloudemu"}

	if _, err := io.Copy(w, strings.NewReader(objContent)); err != nil {
		t.Fatalf("Writer.Copy: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Writer.Close: %v", err)
	}

	// Download object via Reader.
	rd, err := bucket.Object("k1").NewReader(ctx)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if !bytes.Equal(got, []byte(objContent)) {
		t.Errorf("body mismatch: got=%q want=%q", got, objContent)
	}

	if err := rd.Close(); err != nil {
		t.Errorf("Reader.Close: %v", err)
	}

	// Get object attributes (metadata).
	attrs, err := bucket.Object("k1").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if attrs.Size != int64(len(objContent)) {
		t.Errorf("Size=%d want %d", attrs.Size, len(objContent))
	}

	if attrs.ContentType != "text/plain" {
		t.Errorf("ContentType=%s want text/plain", attrs.ContentType)
	}

	// List objects in the bucket.
	it := bucket.Objects(ctx, nil)

	seen := map[string]bool{}
	for {
		a, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("list: %v", err)
		}

		seen[a.Name] = true
	}

	if !seen["k1"] {
		t.Errorf("k1 not in object list: %v", seen)
	}

	// Delete object.
	if err := bucket.Object("k1").Delete(ctx); err != nil {
		t.Errorf("delete object: %v", err)
	}

	// Delete bucket.
	if err := bucket.Delete(ctx); err != nil {
		t.Errorf("delete bucket: %v", err)
	}
}
