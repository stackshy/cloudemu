package logging

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// CreateGroup creates a log group. OCI-only: the portable driver has no
// compartment or description.
func (m *Mock) CreateGroup(_ context.Context, spec LogGroupSpec) (*LogGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createGroup(spec)
}

// createGroup is CreateGroup with mu already held.
func (m *Mock) createGroup(spec LogGroupSpec) (*LogGroup, error) {
	if err := requireName(spec.DisplayName, "log group displayName"); err != nil {
		return nil, err
	}

	if _, ok := m.groupByName(spec.DisplayName); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "log group %q already exists", spec.DisplayName)
	}

	retention := spec.RetentionDays
	if retention == 0 {
		retention = defaultRetentionDays
	}

	now := m.now()
	g := &LogGroup{
		ID:               m.newOCID(typeLogGroup),
		CompartmentID:    m.compartmentOr(spec.CompartmentID),
		DisplayName:      spec.DisplayName,
		Description:      spec.Description,
		LifecycleState:   StateActive,
		TimeCreated:      now,
		TimeLastModified: now,
		FreeformTags:     copyTags(spec.FreeformTags),
		RetentionDays:    retention,
	}

	m.groups.Set(g.ID, g)

	out := *g

	return &out, nil
}

// GetGroup returns a log group by OCID.
func (m *Mock) GetGroup(_ context.Context, id string) (*LogGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.groups.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", id)
	}

	out := *g

	return &out, nil
}

// ListGroups returns the log groups in a compartment, optionally narrowed to
// one display name. Matching is exact: real OCI descends the compartment tree
// only when the caller asks it to.
func (m *Mock) ListGroups(_ context.Context, compartmentID, displayName string) ([]LogGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := requireName(compartmentID, "compartmentId"); err != nil {
		return nil, err
	}

	out := make([]LogGroup, 0, m.groups.Len())

	for _, g := range m.groups.SortedValues() {
		if g.CompartmentID != compartmentID {
			continue
		}

		if displayName != "" && g.DisplayName != displayName {
			continue
		}

		out = append(out, *g)
	}

	return out, nil
}

// UpdateGroup replaces the mutable fields of a log group.
func (m *Mock) UpdateGroup(_ context.Context, id string, u LogGroupUpdate) (*LogGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groups.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", id)
	}

	if u.DisplayName != nil && *u.DisplayName != g.DisplayName {
		if _, taken := m.groupByName(*u.DisplayName); taken {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "log group %q already exists", *u.DisplayName)
		}

		g.DisplayName = *u.DisplayName
	}

	if u.Description != nil {
		g.Description = *u.Description
	}

	if u.FreeformTags != nil {
		g.FreeformTags = copyTags(u.FreeformTags)
	}

	g.TimeLastModified = m.now()

	out := *g

	return &out, nil
}

// DeleteGroup deletes a log group and the logs inside it. Deleting the group
// discards their entries with them.
func (m *Mock) DeleteGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.groups.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "log group %q not found", id)
	}

	for _, rec := range m.logsIn(id) {
		m.logs.Delete(rec.log.ID)
	}

	m.groups.Delete(id)

	return nil
}

// MoveGroup moves a log group and its logs to another compartment.
func (m *Mock) MoveGroup(_ context.Context, id, compartmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireName(compartmentID, "compartmentId"); err != nil {
		return err
	}

	g, ok := m.groups.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "log group %q not found", id)
	}

	g.CompartmentID = compartmentID
	g.TimeLastModified = m.now()

	for _, rec := range m.logsIn(id) {
		rec.log.CompartmentID = compartmentID

		if rec.log.Configuration != nil {
			rec.log.Configuration.CompartmentID = compartmentID
		}
	}

	return nil
}
