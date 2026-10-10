package s3

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestGetHeadCurrentDeleteMarker verifies a plain GET/HEAD of a key whose
// current version is a delete marker reports the marker (id and timestamp)
// while still reading as NotFound for library callers.
func TestGetHeadCurrentDeleteMarker(t *testing.T) {
	for _, status := range []string{versioningEnabled, versioningSuspended} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			m := newTestMock()

			requireNoError(t, m.CreateBucket(ctx, "b"))
			requireNoError(t, m.SetVersioningStatus(ctx, "b", status))
			requireNoError(t, m.PutObject(ctx, "b", "k", []byte("x"), "text/plain", nil))

			markerID, marker, err := m.DeleteObjectVersion(ctx, "b", "k", "")
			requireNoError(t, err)
			assertEqual(t, true, marker)

			_, getErr := m.GetObject(ctx, "b", "k")
			_, headErr := m.HeadObject(ctx, "b", "k")

			for _, err := range []error{getErr, headErr} {
				var dm *driver.DeleteMarkerError
				if !errors.As(err, &dm) {
					t.Fatalf("err = %v (%T), want *driver.DeleteMarkerError", err, err)
				}

				assertEqual(t, markerID, dm.VersionID)

				if dm.LastModified == "" {
					t.Fatal("DeleteMarkerError.LastModified is empty")
				}

				if !cerrors.IsNotFound(err) {
					t.Fatalf("delete-marker error does not read as NotFound: %v", err)
				}
			}
		})
	}
}

// TestGetMissingKeyIsPlainNotFound verifies a key that never existed, or was
// deleted on an unversioned bucket, is a plain NotFound without marker info.
func TestGetMissingKeyIsPlainNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("x"), "text/plain", nil))
	requireNoError(t, m.DeleteObject(ctx, "b", "k"))

	for _, key := range []string{"k", "never"} {
		_, err := m.GetObject(ctx, "b", key)
		if !cerrors.IsNotFound(err) || errors.Is(err, driver.ErrDeleteMarker) {
			t.Fatalf("GetObject(%q) err = %v, want plain NotFound", key, err)
		}
	}
}
