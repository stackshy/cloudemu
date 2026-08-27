package s3

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestPutObjectConditionalIfNoneMatch verifies create-if-absent semantics: the
// first If-None-Match:"*" write on a fresh key succeeds; a second one over the
// now-existing key fails with FailedPrecondition and does NOT overwrite.
func TestPutObjectConditionalIfNoneMatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "cb"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	pre := driver.S3PutPrecondition{IfNoneMatch: "*"}

	if _, err := m.PutObjectConditional(ctx, "cb", "k", []byte("v1"), "text/plain", nil, pre); err != nil {
		t.Fatalf("first create-if-absent: %v", err)
	}

	_, err := m.PutObjectConditional(ctx, "cb", "k", []byte("v2"), "text/plain", nil, pre)
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("second create-if-absent err = %v, want FailedPrecondition", err)
	}

	// The clobbering write must not have landed.
	obj, err := m.GetObject(ctx, "cb", "k")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(obj.Data) != "v1" {
		t.Fatalf("body = %q, want v1 (failed condition must not overwrite)", obj.Data)
	}
}

// TestPutObjectConditionalIfMatch verifies optimistic replace: an If-Match with
// the current ETag succeeds; a stale ETag fails without overwriting; an If-Match
// on a missing key fails.
func TestPutObjectConditionalIfMatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "cb"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := m.PutObject(ctx, "cb", "k", []byte("v1"), "text/plain", nil); err != nil {
		t.Fatalf("seed PutObject: %v", err)
	}

	info, err := m.HeadObject(ctx, "cb", "k")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	// Matching ETag -> success.
	if _, err := m.PutObjectConditional(ctx, "cb", "k",
		[]byte("v2"), "text/plain", nil, driver.S3PutPrecondition{IfMatch: info.ETag}); err != nil {
		t.Fatalf("If-Match matching: %v", err)
	}

	// Stale ETag -> 412, no overwrite.
	_, err = m.PutObjectConditional(ctx, "cb", "k",
		[]byte("v3"), "text/plain", nil, driver.S3PutPrecondition{IfMatch: "0000000000000000000000000000dead"})
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("If-Match stale err = %v, want FailedPrecondition", err)
	}
	obj, err := m.GetObject(ctx, "cb", "k")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(obj.Data) != "v2" {
		t.Fatalf("body = %q, want v2", obj.Data)
	}

	// If-Match on a missing key -> 412.
	_, err = m.PutObjectConditional(ctx, "cb", "missing",
		[]byte("x"), "text/plain", nil, driver.S3PutPrecondition{IfMatch: info.ETag})
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("If-Match on missing key err = %v, want FailedPrecondition", err)
	}
}

// TestPutObjectConditionalConcurrentCreate hammers the same fresh key with many
// concurrent create-if-absent writers: exactly one must win, proving the guard
// and the store are evaluated atomically (run with -race). A Get-then-Put
// implementation would let several racing writers all observe "absent" and win.
func TestPutObjectConditionalConcurrentCreate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "cb"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	const writers = 32
	var (
		wins  atomic.Int64
		start = make(chan struct{})
		wg    sync.WaitGroup
	)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := m.PutObjectConditional(ctx, "cb", "race",
				[]byte("v"), "text/plain", nil, driver.S3PutPrecondition{IfNoneMatch: "*"})
			if err == nil {
				wins.Add(1)
			} else if !cerrors.IsFailedPrecondition(err) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("winners = %d, want exactly 1 (atomic create-if-absent)", got)
	}
}
