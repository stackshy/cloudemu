package guardduty

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
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

// validateSetInput enforces the required Name/Format/Location fields real
// GuardDuty rejects with a BadRequestException when absent.
func validateSetInput(name, format, location string) error {
	if name == "" {
		return badRequest("name is required")
	}

	if format == "" {
		return badRequest("format is required")
	}

	if location == "" {
		return badRequest("location is required")
	}

	return nil
}

// CreateIPSet creates an IP set under a detector. The detector's lock is held
// across the parent-existence check and the child insert so a concurrent
// DeleteDetector cannot orphan it.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateIPSet(_ context.Context, in driver.CreateIPSetInput) (id string, err error) {
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

	dd.ipSets[setID] = driver.IPSet{
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

// GetIPSet returns a deep copy of a stored IP set.
func (m *Mock) GetIPSet(_ context.Context, detectorID, ipSetID string) (*driver.IPSet, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	s, ok := dd.ipSets[ipSetID]
	if !ok {
		return nil, notFound("The request is rejected because the input ipSetId is not found: %s", ipSetID)
	}

	out := copyIPSet(s)

	return &out, nil
}

// UpdateIPSet patches an IP set's mutable fields. Nil pointers are left
// unchanged.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) UpdateIPSet(_ context.Context, in driver.UpdateIPSetInput) error {
	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	s, ok := dd.ipSets[in.IPSetID]
	if !ok {
		return notFound("The request is rejected because the input ipSetId is not found: %s", in.IPSetID)
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
	dd.ipSets[in.IPSetID] = s

	return nil
}

// DeleteIPSet removes an IP set from its detector.
func (m *Mock) DeleteIPSet(_ context.Context, detectorID, ipSetID string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.ipSets[ipSetID]; !ok {
		return notFound("The request is rejected because the input ipSetId is not found: %s", ipSetID)
	}

	delete(dd.ipSets, ipSetID)

	return nil
}

// ListIPSets lists a detector's IP-set IDs, sorted for deterministic output.
func (m *Mock) ListIPSets(_ context.Context, detectorID string, page driver.Page) (ids []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	dd.mu.RLock()
	all := make([]string, 0, len(dd.ipSets))
	for id := range dd.ipSets {
		all = append(all, id)
	}
	dd.mu.RUnlock()

	sort.Strings(all)

	return paginateIDs(all, page)
}
