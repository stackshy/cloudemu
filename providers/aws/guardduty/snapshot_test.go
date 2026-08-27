package guardduty_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// TestSnapshotRoundTripGuardDuty proves a snapshot/restore round-trip preserves
// a detector together with its nested child state (a filter lives in the
// detector's unexported filters map) under their original identities.
func TestSnapshotRoundTripGuardDuty(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	det, err := src.CreateDetector(ctx, driver.CreateDetectorInput{
		Enable: true,
		Tags:   map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	if _, err := src.CreateFilter(ctx, driver.CreateFilterInput{
		DetectorID:      det.ID,
		Name:            "f1",
		FindingCriteria: []byte(`{"criterion":{}}`),
	}); err != nil {
		t.Fatalf("CreateFilter: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	gotDet, err := dst.GetDetector(ctx, det.ID)
	if err != nil {
		t.Fatalf("GetDetector: %v", err)
	}

	if gotDet.Tags["env"] != "prod" {
		t.Fatalf("restored detector tags = %+v", gotDet.Tags)
	}

	// The filter is nested inside the detector's unexported filters map; confirm
	// the promotion carried it across.
	f, err := dst.GetFilter(ctx, det.ID, "f1")
	if err != nil {
		t.Fatalf("GetFilter: %v", err)
	}

	if f.Name != "f1" {
		t.Fatalf("restored filter = %+v", f)
	}
}
