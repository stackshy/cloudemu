package guardduty //nolint:dupl // near-identical to the sibling list-set files by API shape; per-type descriptors beat reflection.

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// copyTrustedEntitySet returns a deep copy of a stored TrustedEntitySet so a
// reader cannot alias its Tags map.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func copyTrustedEntitySet(s driver.TrustedEntitySet) driver.TrustedEntitySet {
	out := s
	out.Tags = copyTags(s.Tags)

	return out
}

// trustedEntitySetStore describes how the generic list-set CRUD helpers store,
// build, patch, and copy a TrustedEntitySet.
//
//nolint:dupl // near-identical descriptor to the sibling list-set resources by API shape.
func trustedEntitySetStore() setStore[driver.TrustedEntitySet] {
	return setStore[driver.TrustedEntitySet]{
		notFoundMsg: "The request is rejected because the input trustedEntitySetId is not found: %s",
		storeOf:     func(dd *detectorData) map[string]driver.TrustedEntitySet { return dd.trustES },
		build: func(id string, in setInput, now time.Time) driver.TrustedEntitySet {
			return driver.TrustedEntitySet{
				ID: id, Name: in.name, Format: in.format, Location: in.location,
				Status: setStatus(in.activate), ExpectedBucketOwner: in.expectedBucketOwner,
				Tags: copyTags(in.tags), CreatedAt: now, UpdatedAt: now,
			}
		},
		apply: func(cur driver.TrustedEntitySet, patch setPatch, now time.Time) driver.TrustedEntitySet {
			applySetPatch(&cur.Name, &cur.Location, &cur.Status, &cur.ExpectedBucketOwner, patch)
			cur.UpdatedAt = now

			return cur
		},
		copy: copyTrustedEntitySet,
	}
}

// CreateTrustedEntitySet creates a trusted-entity set under a detector, holding
// the detector's lock across the parent check and the child insert.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateTrustedEntitySet(_ context.Context, in driver.CreateTrustedEntitySetInput) (id string, err error) {
	return createSet(m, trustedEntitySetStore(), setInput{
		detectorID: in.DetectorID, name: in.Name, format: in.Format, location: in.Location,
		activate: in.Activate, expectedBucketOwner: in.ExpectedBucketOwner, tags: in.Tags,
	})
}

// GetTrustedEntitySet returns a deep copy of a stored trusted-entity set.
func (m *Mock) GetTrustedEntitySet(_ context.Context, detectorID, setID string) (*driver.TrustedEntitySet, error) {
	return getSet(m, trustedEntitySetStore(), detectorID, setID)
}

// UpdateTrustedEntitySet patches a trusted-entity set's mutable fields.
func (m *Mock) UpdateTrustedEntitySet(_ context.Context, in driver.UpdateTrustedEntitySetInput) error {
	return updateSet(m, trustedEntitySetStore(), in.DetectorID, in.TrustedEntitySetID, setPatch{
		name: in.Name, location: in.Location, activate: in.Activate,
		expectedBucketOwner: in.ExpectedBucketOwner,
	})
}

// DeleteTrustedEntitySet removes a trusted-entity set from its detector.
func (m *Mock) DeleteTrustedEntitySet(_ context.Context, detectorID, setID string) error {
	return deleteSet(m, trustedEntitySetStore(), detectorID, setID)
}

// ListTrustedEntitySets lists a detector's trusted-entity-set IDs, sorted.
func (m *Mock) ListTrustedEntitySets(
	_ context.Context, detectorID string, page driver.Page,
) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	return listChildIDs(dd, dd.trustES, page)
}
