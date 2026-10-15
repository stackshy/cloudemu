package s3

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestMultipartUploadIsBoundToItsKey checks every multipart operation
// answers NotFound (NoSuchUpload on the wire) when an upload ID is sent with a
// key other than the one it was created for, and leaves the upload intact.
func TestMultipartUploadIsBoundToItsKey(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	requireNoError(t, m.CreateBucket(ctx, "b"))

	mp, err := m.CreateMultipartUpload(ctx, "b", "private/victim", "text/plain")
	requireNoError(t, err)

	part, err := m.UploadPart(ctx, "b", "private/victim", mp.UploadID, 1, []byte("secret"))
	requireNoError(t, err)

	const other = "shared/public/mine"

	wantNotFound := func(what string, err error) {
		t.Helper()

		if !cerrors.IsNotFound(err) {
			t.Fatalf("%s with another key: err = %v, want NotFound", what, err)
		}
	}

	_, err = m.UploadPart(ctx, "b", other, mp.UploadID, 2, []byte("x"))
	wantNotFound("UploadPart", err)

	_, err = m.ListParts(ctx, "b", other, mp.UploadID)
	wantNotFound("ListParts", err)

	wantNotFound("CompleteMultipartUpload",
		m.CompleteMultipartUpload(ctx, "b", other, mp.UploadID, []driver.UploadPart{{PartNumber: 1, ETag: part.ETag}}))
	wantNotFound("AbortMultipartUpload", m.AbortMultipartUpload(ctx, "b", other, mp.UploadID))

	if _, err := m.GetObject(ctx, "b", other); err == nil {
		t.Fatal("a complete with another key wrote the upload's bytes to that key")
	}

	parts, err := m.ListParts(ctx, "b", "private/victim", mp.UploadID)
	requireNoError(t, err)

	if len(parts) != 1 {
		t.Fatalf("the upload has %d parts, want 1 (untouched)", len(parts))
	}

	requireNoError(t, m.CompleteMultipartUpload(ctx, "b", "private/victim", mp.UploadID,
		[]driver.UploadPart{{PartNumber: 1, ETag: part.ETag}}))
}
