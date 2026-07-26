package s3

import (
	"context"
	"strconv"
	"sync"
	"testing"
)

// TestListPartsConcurrentWithUploadPart exercises ListParts against concurrent
// UploadPart writes on the same upload (as the SDK's manager.Uploader does).
// Run with -race it proves the parts map is synchronized; without the mutex it
// trips "concurrent map iteration and map write".
func TestListPartsConcurrentWithUploadPart(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	const bucket = "race"
	if err := m.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	up, err := m.CreateMultipartUpload(ctx, bucket, "key", "")
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	uploadID := up.UploadID

	const n = 50
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(2)
		go func(pn int) {
			defer wg.Done()
			_, _ = m.UploadPart(ctx, bucket, "key", uploadID, pn, []byte(strconv.Itoa(pn)))
		}(i)
		go func() {
			defer wg.Done()
			_, _ = m.ListParts(ctx, bucket, "key", uploadID)
		}()
	}
	wg.Wait()

	parts, err := m.ListParts(ctx, bucket, "key", uploadID)
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != n {
		t.Fatalf("final parts = %d, want %d", len(parts), n)
	}
}
