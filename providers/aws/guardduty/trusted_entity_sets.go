package guardduty

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
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

// CreateTrustedEntitySet creates a trusted-entity set under a detector, holding
// the detector's lock across the parent check and the child insert.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateTrustedEntitySet(_ context.Context, in driver.CreateTrustedEntitySetInput) (id string, err error) {
	if verr := validateSetInput(in.Name, in.Format, in.Location); verr != nil {
		return "", verr
	}

	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return "", err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	now := m.now()
	setID := idgen.GenerateID("")

	dd.trustES[setID] = driver.TrustedEntitySet{
		ID:                  setID,
		Name:                in.Name,
		Format:              in.Format,
		Location:            in.Location,
		Status:              setStatus(in.Activate),
		ExpectedBucketOwner: in.ExpectedBucketOwner,
		Tags:                copyTags(in.Tags),
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	return setID, nil
}

// GetTrustedEntitySet returns a deep copy of a stored trusted-entity set.
func (m *Mock) GetTrustedEntitySet(_ context.Context, detectorID, setID string) (*driver.TrustedEntitySet, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	s, ok := dd.trustES[setID]
	if !ok {
		return nil, notFound("The request is rejected because the input trustedEntitySetId is not found: %s", setID)
	}

	out := copyTrustedEntitySet(s)

	return &out, nil
}

// UpdateTrustedEntitySet patches a trusted-entity set's mutable fields.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) UpdateTrustedEntitySet(_ context.Context, in driver.UpdateTrustedEntitySetInput) error {
	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	s, ok := dd.trustES[in.TrustedEntitySetID]
	if !ok {
		return notFound("The request is rejected because the input trustedEntitySetId is not found: %s", in.TrustedEntitySetID)
	}

	if in.Name != nil {
		s.Name = *in.Name
	}

	if in.Location != nil {
		s.Location = *in.Location
	}

	if in.Activate != nil {
		s.Status = setStatus(*in.Activate)
	}

	if in.ExpectedBucketOwner != nil {
		s.ExpectedBucketOwner = *in.ExpectedBucketOwner
	}

	s.UpdatedAt = m.now()
	dd.trustES[in.TrustedEntitySetID] = s

	return nil
}

// DeleteTrustedEntitySet removes a trusted-entity set from its detector.
func (m *Mock) DeleteTrustedEntitySet(_ context.Context, detectorID, setID string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.trustES[setID]; !ok {
		return notFound("The request is rejected because the input trustedEntitySetId is not found: %s", setID)
	}

	delete(dd.trustES, setID)

	return nil
}

// ListTrustedEntitySets lists a detector's trusted-entity-set IDs, sorted.
func (m *Mock) ListTrustedEntitySets(
	_ context.Context, detectorID string, page driver.Page,
) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	dd.mu.RLock()
	all := make([]string, 0, len(dd.trustES))
	for id := range dd.trustES {
		all = append(all, id)
	}
	dd.mu.RUnlock()

	sort.Strings(all)

	return paginateIDs(all, page)
}
