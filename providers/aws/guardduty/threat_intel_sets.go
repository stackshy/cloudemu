package guardduty

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
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

// CreateThreatIntelSet creates a threat-intel set under a detector, holding the
// detector's lock across the parent check and the child insert.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateThreatIntelSet(_ context.Context, in driver.CreateThreatIntelSetInput) (id string, err error) {
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

	dd.threatIS[setID] = driver.ThreatIntelSet{
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

// GetThreatIntelSet returns a deep copy of a stored threat-intel set.
func (m *Mock) GetThreatIntelSet(_ context.Context, detectorID, setID string) (*driver.ThreatIntelSet, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	s, ok := dd.threatIS[setID]
	if !ok {
		return nil, notFound("The request is rejected because the input threatIntelSetId is not found: %s", setID)
	}

	out := copyThreatIntelSet(s)

	return &out, nil
}

// UpdateThreatIntelSet patches a threat-intel set's mutable fields.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) UpdateThreatIntelSet(_ context.Context, in driver.UpdateThreatIntelSetInput) error {
	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	s, ok := dd.threatIS[in.ThreatIntelSetID]
	if !ok {
		return notFound("The request is rejected because the input threatIntelSetId is not found: %s", in.ThreatIntelSetID)
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
	dd.threatIS[in.ThreatIntelSetID] = s

	return nil
}

// DeleteThreatIntelSet removes a threat-intel set from its detector.
func (m *Mock) DeleteThreatIntelSet(_ context.Context, detectorID, setID string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.threatIS[setID]; !ok {
		return notFound("The request is rejected because the input threatIntelSetId is not found: %s", setID)
	}

	delete(dd.threatIS, setID)

	return nil
}

// ListThreatIntelSets lists a detector's threat-intel-set IDs, sorted.
func (m *Mock) ListThreatIntelSets(
	_ context.Context, detectorID string, page driver.Page,
) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	dd.mu.RLock()
	all := make([]string, 0, len(dd.threatIS))
	for id := range dd.threatIS {
		all = append(all, id)
	}
	dd.mu.RUnlock()

	sort.Strings(all)

	return paginateIDs(all, page)
}
