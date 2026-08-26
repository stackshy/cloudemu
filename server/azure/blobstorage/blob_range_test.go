package blobstorage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
)

// blockID base64-encodes a block name into the fixed-length id the azblob
// block-blob client requires.
func blockID(name string) string {
	return base64.StdEncoding.EncodeToString([]byte(name))
}

// streamOf wraps a string as the ReadSeekCloser StageBlock expects.
func streamOf(s string) io.ReadSeekCloser {
	return streaming.NopCloser(bytes.NewReader([]byte(s)))
}

// rawResponse issues a bare HTTP request through the test transport and returns
// the status, response headers, and body, so wire behaviors the typed SDK
// hides (partial-content status, Content-Range) can be asserted directly.
func (e *blobEnv) rawResponse(t *testing.T, method, path string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()

	req, err := http.NewRequest(method, e.base+path, nil)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e.tr.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if err != nil {
		t.Fatalf("read body %s %s: %v", method, path, err)
	}

	return resp.StatusCode, resp.Header, body
}

func TestSDKRangedGetReturns206(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	const content = "0123456789abcdef"
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte(content), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	status, hdr, body := e.rawResponse(t, http.MethodGet, "/c1/k1", map[string]string{"x-ms-range": "bytes=4-9"})
	if status != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", status)
	}

	if got := hdr.Get("Content-Range"); got != "bytes 4-9/16" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes 4-9/16")
	}

	if got := hdr.Get("Content-Length"); got != "6" {
		t.Fatalf("Content-Length = %q, want 6", got)
	}

	if string(body) != "456789" {
		t.Fatalf("body = %q, want %q", body, "456789")
	}

	if got := hdr.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
}

func TestSDKRangedGetOpenEnded(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	const content = "0123456789abcdef"
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte(content), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	status, hdr, body := e.rawResponse(t, http.MethodGet, "/c1/k1", map[string]string{"Range": "bytes=10-"})
	if status != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", status)
	}

	if got := hdr.Get("Content-Range"); got != "bytes 10-15/16" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes 10-15/16")
	}

	if string(body) != "abcdef" {
		t.Fatalf("body = %q, want %q", body, "abcdef")
	}
}

func TestSDKUnsatisfiableRangeReturns416(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	const content = "0123456789"
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte(content), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	status, hdr, _ := e.rawResponse(t, http.MethodGet, "/c1/k1", map[string]string{"x-ms-range": "bytes=100-200"})
	if status != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", status)
	}

	if got := hdr.Get("Content-Range"); got != "bytes */10" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes */10")
	}
}

// TestSDKRangedDownloadAssemblesOriginal is the data-corruption guard: the
// azblob DownloadBuffer issues many small parallel ranged GETs and copies each
// into the destination at its offset. If a ranged GET returned the full blob
// (the bug) instead of just its slice, the assembled buffer would be corrupt.
func TestSDKRangedDownloadAssemblesOriginal(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	original := make([]byte, 1024)
	for i := range original {
		original[i] = byte('A' + (i % 26))
	}

	if _, err := e.svc.UploadBuffer(ctx, "c1", "big", original, nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	dst := make([]byte, len(original))

	n, err := e.svc.DownloadBuffer(ctx, "c1", "big", dst, &azblob.DownloadBufferOptions{
		BlockSize:   64,
		Concurrency: 8,
	})
	if err != nil {
		t.Fatalf("DownloadBuffer: %v", err)
	}

	if n != int64(len(original)) {
		t.Fatalf("downloaded %d bytes, want %d", n, len(original))
	}

	if !bytes.Equal(dst, original) {
		t.Fatalf("assembled buffer does not equal original")
	}
}

func TestSDKContentPropertiesRoundTrip(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	_, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("payload"), &azblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{
			BlobCacheControl:       to.Ptr("max-age=99"),
			BlobContentEncoding:    to.Ptr("gzip"),
			BlobContentLanguage:    to.Ptr("en-US"),
			BlobContentDisposition: to.Ptr("attachment; filename=x.txt"),
			BlobContentType:        to.Ptr("text/plain"),
		},
	})
	if err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	props, err := e.blob(t, "/c1/k1").GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	assertPtr(t, "CacheControl", props.CacheControl, "max-age=99")
	assertPtr(t, "ContentEncoding", props.ContentEncoding, "gzip")
	assertPtr(t, "ContentLanguage", props.ContentLanguage, "en-US")
	assertPtr(t, "ContentDisposition", props.ContentDisposition, "attachment; filename=x.txt")

	// The same properties must also come back as headers on a read. Use HEAD:
	// a GET carries Content-Encoding: gzip, which the Go transport would try to
	// transparently gunzip over the (uncompressed) test payload.
	_, hdr, _ := e.rawResponse(t, http.MethodHead, "/c1/k1", nil)
	if got := hdr.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("HEAD Content-Encoding = %q, want gzip", got)
	}

	if got := hdr.Get("Cache-Control"); got != "max-age=99" {
		t.Fatalf("HEAD Cache-Control = %q, want max-age=99", got)
	}

	if got := hdr.Get("Content-Disposition"); got != "attachment; filename=x.txt" {
		t.Fatalf("HEAD Content-Disposition = %q, want %q", got, "attachment; filename=x.txt")
	}
}

func assertPtr(t *testing.T, name string, got *string, want string) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s = nil, want %q", name, want)
	}

	if *got != want {
		t.Fatalf("%s = %q, want %q", name, *got, want)
	}
}

// TestSDKCommitBlockListAppendsCommittedBlock exercises the "append by
// re-committing an existing committed block plus a freshly staged one" pattern:
// after the first commit clears staging, the second commit still resolves the
// old block from the committed list (Latest falls back to committed).
func TestSDKCommitBlockListAppendsCommittedBlock(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	bb := e.blockBlob(t, "/c1/appended")

	idA := blockID("block-000a")
	idB := blockID("block-000b")

	if _, err := bb.StageBlock(ctx, idA, streamOf("hello "), nil); err != nil {
		t.Fatalf("StageBlock a: %v", err)
	}

	if _, err := bb.CommitBlockList(ctx, []string{idA}, nil); err != nil {
		t.Fatalf("CommitBlockList a: %v", err)
	}

	if _, err := bb.StageBlock(ctx, idB, streamOf("world"), nil); err != nil {
		t.Fatalf("StageBlock b: %v", err)
	}

	// Re-commit the already-committed block A plus the new block B.
	if _, err := bb.CommitBlockList(ctx, []string{idA, idB}, nil); err != nil {
		t.Fatalf("CommitBlockList a+b: %v", err)
	}

	if got := e.download(t, "appended"); got != "hello world" {
		t.Fatalf("appended blob = %q, want %q", got, "hello world")
	}
}
