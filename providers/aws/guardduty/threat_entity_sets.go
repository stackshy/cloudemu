package guardduty

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
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

// CreateThreatEntitySet creates a threat-entity set under a detector, holding
// the detector's lock across the parent check and the child insert.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateThreatEntitySet(_ context.Context, in driver.CreateThreatEntitySetInput) (id string, err error) {
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

	dd.threatES[setID] = driver.ThreatEntitySet{
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

// GetThreatEntitySet returns a deep copy of a stored threat-entity set.
func (m *Mock) GetThreatEntitySet(_ context.Context, detectorID, setID string) (*driver.ThreatEntitySet, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	s, ok := dd.threatES[setID]
	if !ok {
		return nil, notFound("The request is rejected because the input threatEntitySetId is not found: %s", setID)
	}

	out := copyThreatEntitySet(s)

	return &out, nil
}

// UpdateThreatEntitySet patches a threat-entity set's mutable fields.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) UpdateThreatEntitySet(_ context.Context, in driver.UpdateThreatEntitySetInput) error {
	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	s, ok := dd.threatES[in.ThreatEntitySetID]
	if !ok {
		return notFound("The request is rejected because the input threatEntitySetId is not found: %s", in.ThreatEntitySetID)
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
	dd.threatES[in.ThreatEntitySetID] = s

	return nil
}

// DeleteThreatEntitySet removes a threat-entity set from its detector.
func (m *Mock) DeleteThreatEntitySet(_ context.Context, detectorID, setID string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.threatES[setID]; !ok {
		return notFound("The request is rejected because the input threatEntitySetId is not found: %s", setID)
	}

	delete(dd.threatES, setID)

	return nil
}

// ListThreatEntitySets lists a detector's threat-entity-set IDs, sorted.
func (m *Mock) ListThreatEntitySets(
	_ context.Context, detectorID string, page driver.Page,
) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	dd.mu.RLock()
	all := make([]string, 0, len(dd.threatES))
	for id := range dd.threatES {
		all = append(all, id)
	}
	dd.mu.RUnlock()

	sort.Strings(all)

	return paginateIDs(all, page)
}
