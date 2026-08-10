package guardduty //nolint:dupl // near-identical to the sibling list-set files by API shape; per-type descriptors beat reflection.

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// copyThreatEntitySet returns a deep copy of a stored ThreatEntitySet so a
// reader cannot alias its Tags map.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func copyThreatEntitySet(s driver.ThreatEntitySet) driver.ThreatEntitySet {
	out := s
	out.Tags = copyTags(s.Tags)

	return out
}

// threatEntitySetStore describes how the generic list-set CRUD helpers store,
// build, patch, and copy a ThreatEntitySet.
//
//nolint:dupl // near-identical descriptor to the sibling list-set resources by API shape.
func threatEntitySetStore() setStore[driver.ThreatEntitySet] {
	return setStore[driver.ThreatEntitySet]{
		notFoundMsg: "The request is rejected because the input threatEntitySetId is not found: %s",
		storeOf:     func(dd *detectorData) map[string]driver.ThreatEntitySet { return dd.threatES },
		build: func(id string, in setInput, now time.Time) driver.ThreatEntitySet {
			return driver.ThreatEntitySet{
				ID: id, Name: in.name, Format: in.format, Location: in.location,
				Status: setStatus(in.activate), ExpectedBucketOwner: in.expectedBucketOwner,
				Tags: copyTags(in.tags), CreatedAt: now, UpdatedAt: now,
			}
		},
		apply: func(cur driver.ThreatEntitySet, patch setPatch, now time.Time) driver.ThreatEntitySet {
			applySetPatch(&cur.Name, &cur.Location, &cur.Status, &cur.ExpectedBucketOwner, patch)
			cur.UpdatedAt = now

			return cur
		},
		copy: copyThreatEntitySet,
	}
}

// CreateThreatEntitySet creates a threat-entity set under a detector, holding
// the detector's lock across the parent check and the child insert.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateThreatEntitySet(_ context.Context, in driver.CreateThreatEntitySetInput) (id string, err error) {
	return createSet(m, threatEntitySetStore(), setInput{
		detectorID: in.DetectorID, name: in.Name, format: in.Format, location: in.Location,
		activate: in.Activate, expectedBucketOwner: in.ExpectedBucketOwner, tags: in.Tags,
	})
}

// GetThreatEntitySet returns a deep copy of a stored threat-entity set.
func (m *Mock) GetThreatEntitySet(_ context.Context, detectorID, setID string) (*driver.ThreatEntitySet, error) {
	return getSet(m, threatEntitySetStore(), detectorID, setID)
}

// UpdateThreatEntitySet patches a threat-entity set's mutable fields.
func (m *Mock) UpdateThreatEntitySet(_ context.Context, in driver.UpdateThreatEntitySetInput) error {
	return updateSet(m, threatEntitySetStore(), in.DetectorID, in.ThreatEntitySetID, setPatch{
		name: in.Name, location: in.Location, activate: in.Activate,
		expectedBucketOwner: in.ExpectedBucketOwner,
	})
}

// DeleteThreatEntitySet removes a threat-entity set from its detector.
func (m *Mock) DeleteThreatEntitySet(_ context.Context, detectorID, setID string) error {
	return deleteSet(m, threatEntitySetStore(), detectorID, setID)
}

// ListThreatEntitySets lists a detector's threat-entity-set IDs, sorted.
func (m *Mock) ListThreatEntitySets(
	_ context.Context, detectorID string, page driver.Page,
) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	return listChildIDs(dd, dd.threatES, page)
}
