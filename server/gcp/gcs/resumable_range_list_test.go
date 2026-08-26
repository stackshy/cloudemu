package gcs_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newGCSTestClient spins up the GCS wire handler behind an httptest server and
// returns a real cloud.google.com/go/storage client pointed at it.
func newGCSTestClient(t *testing.T) *storage.Client {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := storage.NewClient(context.Background(),
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func writeGCSObject(t *testing.T, ctx context.Context, bucket *storage.BucketHandle, name string, data []byte) {
	t.Helper()

	w := bucket.Object(name).NewWriter(ctx)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close %q: %v", name, err)
	}
}

// TestSDKGCSResumableUpload verifies a >16MiB object (which the SDK sends as a
// multi-chunk resumable upload by default) round-trips byte-for-byte.
func TestSDKGCSResumableUpload(t *testing.T) {
	client := newGCSTestClient(t)
	ctx := context.Background()

	bucket := client.Bucket("b1")
	if err := bucket.Create(ctx, "p1", nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	// 17 MiB > the 16 MiB default chunk size, so the SDK uploads it resumably
	// across multiple Content-Range chunks that the handler must reassemble.
	const size = 17 << 20

	want := make([]byte, size)
	for i := range want {
		want[i] = byte(i*31 + 7)
	}

	w := bucket.Object("big").NewWriter(ctx)
	w.ContentType = "application/octet-stream"

	if _, err := w.Write(want); err != nil {
		t.Fatalf("Writer.Write: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Writer.Close: %v", err)
	}

	rd, err := bucket.Object("big").NewReader(ctx)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	_ = rd.Close()

	if len(got) != size {
		t.Fatalf("size mismatch: got=%d want=%d", len(got), size)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("resumable upload assembled incorrectly: bytes differ")
	}

	attrs, err := bucket.Object("big").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if attrs.Size != int64(size) {
		t.Errorf("Attrs.Size=%d want %d", attrs.Size, size)
	}
}

// TestResumableGappedChunkDoesNotTruncate drives the resumable protocol
// directly (the SDK only ever sends contiguous chunks) to prove that a chunk
// arriving after a gap is never accepted as final: it must not commit a
// truncated object even when its Content-Range claims to carry the last byte.
func TestResumableGappedChunkDoesNotTruncate(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

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
	if err := bucket.Create(ctx, "p1", nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	httpc := ts.Client()

	// Initialise a resumable session and read back its session URI.
	initReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		ts.URL+"/upload/storage/v1/b/b1/o?uploadType=resumable&name=gapped",
		strings.NewReader(`{"name":"gapped"}`))
	initReq.Header.Set("Content-Type", "application/json")

	initResp, err := httpc.Do(initReq)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	_ = initResp.Body.Close()

	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("init status=%d want 200", initResp.StatusCode)
	}

	loc := initResp.Header.Get("Location")
	if loc == "" {
		t.Fatal("init returned no Location session URI")
	}

	// resumeIncomplete asserts a chunk POST was acknowledged as "resume
	// incomplete" (200 + X-Http-Status-Code-Override: 308) rather than committing
	// the object.
	sendChunk := func(contentRange string, body []byte) *http.Response {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, loc, bytes.NewReader(body))
		req.Header.Set("Content-Range", contentRange)
		req.Header.Set("X-Guploader-No-308", "yes")

		resp, err := httpc.Do(req)
		if err != nil {
			t.Fatalf("chunk %q: %v", contentRange, err)
		}

		return resp
	}

	// First chunk (0-99) with the total still unknown → resume incomplete.
	r1 := sendChunk("bytes 0-99/*", bytes.Repeat([]byte("A"), 100))
	_ = r1.Body.Close()

	if got := r1.Header.Get("X-Http-Status-Code-Override"); got != "308" {
		t.Fatalf("first chunk override=%q want 308 (should be resume-incomplete)", got)
	}

	// Second chunk skips 100-199 and claims the final byte of a 300-byte object.
	// It must NOT be committed as a (truncated) object.
	r2 := sendChunk("bytes 200-299/300", bytes.Repeat([]byte("B"), 100))
	_ = r2.Body.Close()

	if r2.Header.Get("X-Http-Status-Code-Override") != "308" {
		t.Fatalf("gapped final chunk was committed (status=%d) — truncation bug", r2.StatusCode)
	}

	// The object must not exist: the upload never completed.
	if _, err := bucket.Object("gapped").Attrs(ctx); err == nil {
		t.Fatal("object was committed despite an incomplete (gapped) upload")
	}
}

// TestSDKGCSRangeReader verifies NewRangeReader returns exactly the requested
// byte slice (a 206 Partial Content on the wire), not the whole body.
func TestSDKGCSRangeReader(t *testing.T) {
	client := newGCSTestClient(t)
	ctx := context.Background()

	bucket := client.Bucket("b1")
	if err := bucket.Create(ctx, "p1", nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	content := []byte("0123456789abcdef")
	writeGCSObject(t, ctx, bucket, "k1", content)

	cases := []struct {
		name          string
		offset, count int64
		want          []byte
	}{
		{name: "mid-range", offset: 3, count: 5, want: content[3:8]},
		{name: "to-end", offset: 10, count: -1, want: content[10:]},
		{name: "suffix", offset: -4, count: -1, want: content[len(content)-4:]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd, err := bucket.Object("k1").NewRangeReader(ctx, tc.offset, tc.count)
			if err != nil {
				t.Fatalf("NewRangeReader(%d,%d): %v", tc.offset, tc.count, err)
			}

			got, err := io.ReadAll(rd)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			_ = rd.Close()

			if !bytes.Equal(got, tc.want) {
				t.Errorf("range bytes mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

// TestSDKGCSListDelimiterPagination verifies a delimiter listing paginated with
// a page size of 1 returns each common prefix exactly once (no duplication
// across pages) and surfaces every top-level object.
func TestSDKGCSListDelimiterPagination(t *testing.T) {
	client := newGCSTestClient(t)
	ctx := context.Background()

	bucket := client.Bucket("b1")
	if err := bucket.Create(ctx, "p1", nil); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	for _, name := range []string{"m1", "m2", "m3", "a/x", "b/x", "c/x"} {
		writeGCSObject(t, ctx, bucket, name, []byte("v"))
	}

	it := bucket.Objects(ctx, &storage.Query{Delimiter: "/"})
	it.PageInfo().MaxSize = 1 // force many small pages

	prefixCount := map[string]int{}
	objNames := map[string]bool{}

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if attrs.Prefix != "" {
			prefixCount[attrs.Prefix]++
			continue
		}

		objNames[attrs.Name] = true
	}

	for _, p := range []string{"a/", "b/", "c/"} {
		if prefixCount[p] != 1 {
			t.Errorf("prefix %q count=%d want 1 (duplicated or missing across pages)", p, prefixCount[p])
		}
	}

	for _, n := range []string{"m1", "m2", "m3"} {
		if !objNames[n] {
			t.Errorf("top-level object %q missing from listing", n)
		}
	}
}

// TestSDKGCSDeleteBucketWithNoncurrentVersions verifies that a bucket holding
// only noncurrent (archived) object versions still refuses deletion, matching
// real GCS's 409 not-empty.
func TestSDKGCSDeleteBucketWithNoncurrentVersions(t *testing.T) {
	client := newGCSTestClient(t)
	ctx := context.Background()

	bucket := client.Bucket("b1")
	if err := bucket.Create(ctx, "p1", &storage.BucketAttrs{VersioningEnabled: true}); err != nil {
		t.Fatalf("bucket.Create: %v", err)
	}

	// Two writes leave one archived generation; deleting the live object leaves
	// no live object but retains the noncurrent version.
	writeGCSObject(t, ctx, bucket, "k1", []byte("v1"))
	writeGCSObject(t, ctx, bucket, "k1", []byte("v2"))

	if err := bucket.Object("k1").Delete(ctx); err != nil {
		t.Fatalf("delete live object: %v", err)
	}

	if err := bucket.Delete(ctx); err == nil {
		t.Errorf("bucket.Delete succeeded but noncurrent versions remain; expected not-empty error")
	}
}
