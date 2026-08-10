package guardduty

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// copyFilter returns a deep copy of a stored Filter so a reader cannot alias its
// Tags map or FindingCriteria raw block.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot a copy of stored state.
func copyFilter(f driver.Filter) driver.Filter {
	out := f
	out.Tags = copyTags(f.Tags)
	out.FindingCriteria = copyRaw(f.FindingCriteria)

	return out
}

// filterAction resolves a filter action, defaulting to NOOP as real GuardDuty
// does when the caller omits it.
func filterAction(action string) string {
	if action == "" {
		return driver.FilterActionNoop
	}

	return action
}

// CreateFilter creates a saved-finding filter under a detector, keyed by its
// (unique) name. A duplicate name is rejected with a ConflictException, matching
// real GuardDuty. The detector's lock is held across the parent check, the
// duplicate check, and the insert so a concurrent DeleteDetector cannot orphan
// it and two racing creates cannot both win.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) CreateFilter(_ context.Context, in driver.CreateFilterInput) (name string, err error) {
	if in.Name == "" {
		return "", badRequest("name is required")
	}

	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return "", err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, exists := dd.filters[in.Name]; exists {
		return "", conflict("A filter with the name %s already exists", in.Name)
	}

	now := m.now()

	dd.filters[in.Name] = driver.Filter{
		Name:            in.Name,
		Action:          filterAction(in.Action),
		Description:     in.Description,
		Rank:            in.Rank,
		FindingCriteria: copyRaw(in.FindingCriteria),
		Tags:            copyTags(in.Tags),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	return in.Name, nil
}

// GetFilter returns a deep copy of a stored filter.
func (m *Mock) GetFilter(_ context.Context, detectorID, filterName string) (*driver.Filter, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	f, ok := dd.filters[filterName]
	if !ok {
		return nil, notFound("The request is rejected because the input filterName is not found: %s", filterName)
	}

	out := copyFilter(f)

	return &out, nil
}

// UpdateFilter patches a filter's mutable fields. Nil pointers are left
// unchanged; FindingCriteria is replaced only when non-nil.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.GuardDuty interface (by-value input).
func (m *Mock) UpdateFilter(_ context.Context, in driver.UpdateFilterInput) (name string, err error) {
	dd, err := m.getDetector(in.DetectorID)
	if err != nil {
		return "", err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	f, ok := dd.filters[in.FilterName]
	if !ok {
		return "", notFound("The request is rejected because the input filterName is not found: %s", in.FilterName)
	}

	if in.Action != nil {
		f.Action = *in.Action
	}

	if in.Description != nil {
		f.Description = *in.Description
	}

	if in.Rank != nil {
		f.Rank = *in.Rank
	}

	if in.FindingCriteria != nil {
		f.FindingCriteria = copyRaw(in.FindingCriteria)
	}

	f.UpdatedAt = m.now()
	dd.filters[in.FilterName] = f

	return in.FilterName, nil
}

// DeleteFilter removes a filter from its detector.
func (m *Mock) DeleteFilter(_ context.Context, detectorID, filterName string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.filters[filterName]; !ok {
		return notFound("The request is rejected because the input filterName is not found: %s", filterName)
	}

	delete(dd.filters, filterName)

	return nil
}

// ListFilters lists a detector's filter names, sorted for deterministic output.
func (m *Mock) ListFilters(
	_ context.Context, detectorID string, page driver.Page,
) (names []string, next string, err error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, "", err
	}

	dd.mu.RLock()
	all := make([]string, 0, len(dd.filters))
	for name := range dd.filters {
		all = append(all, name)
	}
	dd.mu.RUnlock()

	sort.Strings(all)

	return paginateIDs(all, page)
}
