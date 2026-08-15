package guardduty

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// copyIPSet returns a deep copy of a stored IPSet so a reader cannot alias its
// Tags map.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func copyIPSet(s driver.IPSet) driver.IPSet {
	out := s
	out.Tags = copyTags(s.Tags)

	return out
}

// validSetFormat reports whether f is a modeled set format. The IpSet,
// ThreatIntelSet, ThreatEntitySet, and TrustedEntitySet format enums all share
// the same values.
func validSetFormat(f string) bool {
	switch f {
	case "TXT", "STIX", "OTX_CSV", "ALIEN_VAULT", "PROOF_POINT", "FIRE_EYE":
		return true
	default:
		return false
	}
}

// validateSetInput enforces the required Name/Format/Location fields real
// GuardDuty rejects with a BadRequestException when absent, and validates Format
// against the modeled enum.
func validateSetInput(name, format, location string) error {
	if name == "" {
		return badRequest("name is required")
	}

	if format == "" {
		return badRequest("format is required")
	}

	if !validSetFormat(format) {
		return badRequest("format %q is invalid", format)
	}

	if location == "" {
		return badRequest("location is required")
	}

	return nil
}

// ipSetStore describes how the generic list-set CRUD helpers store, build, patch,
// and copy an IPSet.
//
//nolint:dupl // near-identical descriptor to the other GuardDuty list-set resources by API shape.
func ipSetStore() setStore[driver.IPSet] {
	return setStore[driver.IPSet]{
		notFoundMsg: "The request is rejected because the input ipSetId is not found: %s",
		storeOf:     func(dd *detectorData) map[string]driver.IPSet { return dd.ipSets },
		build: func(id string, in setInput, now time.Time) driver.IPSet {
			return driver.IPSet{
				ID: id, Name: in.name, Format: in.format, Location: in.location,
				Status: setStatus(in.activate), ExpectedBucketOwner: in.expectedBucketOwner,
				Tags: copyTags(in.tags), CreatedAt: now, UpdatedAt: now,
			}
		},
		apply: func(cur driver.IPSet, patch setPatch, now time.Time) driver.IPSet {
			applySetPatch(&cur.Name, &cur.Location, &cur.Status, &cur.ExpectedBucketOwner, patch)
			cur.UpdatedAt = now

			return cur
		},
		copy: copyIPSet,
	}
}

// CreateIPSet creates an IP set under a detector. The detector's lock is held
// across the parent-existence check and the child insert so a concurrent
// DeleteDetector cannot orphan it.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateIPSet(_ context.Context, in driver.CreateIPSetInput) (id string, err error) {
	return createSet(m, ipSetStore(), setInput{
		detectorID: in.DetectorID, name: in.Name, format: in.Format, location: in.Location,
		activate: in.Activate, expectedBucketOwner: in.ExpectedBucketOwner, tags: in.Tags,
	})
}

// GetIPSet returns a deep copy of a stored IP set.
func (m *Mock) GetIPSet(_ context.Context, detectorID, ipSetID string) (*driver.IPSet, error) {
	return getSet(m, ipSetStore(), detectorID, ipSetID)
}

// UpdateIPSet patches an IP set's mutable fields. Nil pointers are left
// unchanged.
func (m *Mock) UpdateIPSet(_ context.Context, in driver.UpdateIPSetInput) error {
	return updateSet(m, ipSetStore(), in.DetectorID, in.IPSetID, setPatch{
		name: in.Name, location: in.Location, activate: in.Activate,
		expectedBucketOwner: in.ExpectedBucketOwner,
	})
}

// DeleteIPSet removes an IP set from its detector.
func (m *Mock) DeleteIPSet(_ context.Context, detectorID, ipSetID string) error {
	return deleteSet(m, ipSetStore(), detectorID, ipSetID)
}

// ListIPSets lists a detector's IP-set IDs, sorted for deterministic output.
func (m *Mock) ListIPSets(_ context.Context, detectorID string, page driver.Page) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	return listChildIDs(dd, dd.ipSets, page)
}
