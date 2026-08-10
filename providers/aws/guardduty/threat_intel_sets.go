package guardduty //nolint:dupl // near-identical to the sibling list-set files by API shape; per-type descriptors beat reflection.

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// copyThreatIntelSet returns a deep copy of a stored ThreatIntelSet so a reader
// cannot alias its Tags map.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func copyThreatIntelSet(s driver.ThreatIntelSet) driver.ThreatIntelSet {
	out := s
	out.Tags = copyTags(s.Tags)

	return out
}

// threatIntelSetStore describes how the generic list-set CRUD helpers store,
// build, patch, and copy a ThreatIntelSet.
//
//nolint:dupl // near-identical descriptor to the sibling list-set resources by API shape.
func threatIntelSetStore() setStore[driver.ThreatIntelSet] {
	return setStore[driver.ThreatIntelSet]{
		notFoundMsg: "The request is rejected because the input threatIntelSetId is not found: %s",
		storeOf:     func(dd *detectorData) map[string]driver.ThreatIntelSet { return dd.threatIS },
		build: func(id string, in setInput, now time.Time) driver.ThreatIntelSet {
			return driver.ThreatIntelSet{
				ID: id, Name: in.name, Format: in.format, Location: in.location,
				Status: setStatus(in.activate), ExpectedBucketOwner: in.expectedBucketOwner,
				Tags: copyTags(in.tags), CreatedAt: now, UpdatedAt: now,
			}
		},
		apply: func(cur driver.ThreatIntelSet, patch setPatch, now time.Time) driver.ThreatIntelSet {
			applySetPatch(&cur.Name, &cur.Location, &cur.Status, &cur.ExpectedBucketOwner, patch)
			cur.UpdatedAt = now

			return cur
		},
		copy: copyThreatIntelSet,
	}
}

// CreateThreatIntelSet creates a threat-intel set under a detector, holding the
// detector's lock across the parent check and the child insert.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateThreatIntelSet(_ context.Context, in driver.CreateThreatIntelSetInput) (id string, err error) {
	return createSet(m, threatIntelSetStore(), setInput{
		detectorID: in.DetectorID, name: in.Name, format: in.Format, location: in.Location,
		activate: in.Activate, expectedBucketOwner: in.ExpectedBucketOwner, tags: in.Tags,
	})
}

// GetThreatIntelSet returns a deep copy of a stored threat-intel set.
func (m *Mock) GetThreatIntelSet(_ context.Context, detectorID, setID string) (*driver.ThreatIntelSet, error) {
	return getSet(m, threatIntelSetStore(), detectorID, setID)
}

// UpdateThreatIntelSet patches a threat-intel set's mutable fields.
func (m *Mock) UpdateThreatIntelSet(_ context.Context, in driver.UpdateThreatIntelSetInput) error {
	return updateSet(m, threatIntelSetStore(), in.DetectorID, in.ThreatIntelSetID, setPatch{
		name: in.Name, location: in.Location, activate: in.Activate,
		expectedBucketOwner: in.ExpectedBucketOwner,
	})
}

// DeleteThreatIntelSet removes a threat-intel set from its detector.
func (m *Mock) DeleteThreatIntelSet(_ context.Context, detectorID, setID string) error {
	return deleteSet(m, threatIntelSetStore(), detectorID, setID)
}

// ListThreatIntelSets lists a detector's threat-intel-set IDs, sorted.
func (m *Mock) ListThreatIntelSets(
	_ context.Context, detectorID string, page driver.Page,
) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	return listChildIDs(dd, dd.threatIS, page)
}
