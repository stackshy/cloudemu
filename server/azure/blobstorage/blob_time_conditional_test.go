package blobstorage_test

import (
	"context"
	"net/http"
	"testing"
)

const (
	pastDate   = "Wed, 01 Jan 2020 00:00:00 GMT"
	futureDate = "Fri, 01 Jan 2100 00:00:00 GMT"
)

func uploadK1(t *testing.T, e *blobEnv) {
	t.Helper()

	if _, err := e.svc.UploadBuffer(context.Background(), "c1", "k1", []byte("payload"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}
}

func TestSDKIfModifiedSinceRead(t *testing.T) {
	e := newBlobEnv(t)
	uploadK1(t, e)

	// Blob was written "now"; it has NOT been modified since the far future -> 304.
	if got := e.rawStatus(t, http.MethodGet, "/c1/k1", map[string]string{"If-Modified-Since": futureDate}); got != http.StatusNotModified {
		t.Fatalf("If-Modified-Since(future) status = %d, want 304", got)
	}

	// It HAS been modified since 2020 -> full 200.
	if got := e.rawStatus(t, http.MethodGet, "/c1/k1", map[string]string{"If-Modified-Since": pastDate}); got != http.StatusOK {
		t.Fatalf("If-Modified-Since(past) status = %d, want 200", got)
	}
}

func TestSDKIfUnmodifiedSinceRead(t *testing.T) {
	e := newBlobEnv(t)
	uploadK1(t, e)

	// Modified after 2020 -> 412.
	if got := e.rawStatus(t, http.MethodGet, "/c1/k1", map[string]string{"If-Unmodified-Since": pastDate}); got != http.StatusPreconditionFailed {
		t.Fatalf("If-Unmodified-Since(past) status = %d, want 412", got)
	}

	// Not modified after 2100 -> 200.
	if got := e.rawStatus(t, http.MethodGet, "/c1/k1", map[string]string{"If-Unmodified-Since": futureDate}); got != http.StatusOK {
		t.Fatalf("If-Unmodified-Since(future) status = %d, want 200", got)
	}
}

func TestSDKIfUnmodifiedSinceWrite(t *testing.T) {
	e := newBlobEnv(t)
	uploadK1(t, e)

	// Overwrite gated on If-Unmodified-Since in the past: the blob has been
	// modified after that time -> 412.
	if got := e.rawStatus(t, http.MethodPut, "/c1/k1", map[string]string{"If-Unmodified-Since": pastDate}); got != http.StatusPreconditionFailed {
		t.Fatalf("PUT If-Unmodified-Since(past) status = %d, want 412", got)
	}

	// With a future guard the overwrite proceeds.
	if got := e.rawStatus(t, http.MethodPut, "/c1/k1", map[string]string{"If-Unmodified-Since": futureDate}); got != http.StatusCreated {
		t.Fatalf("PUT If-Unmodified-Since(future) status = %d, want 201", got)
	}
}
