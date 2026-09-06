package appflow

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// CreateConnectorProfile provisions a new connector profile with a stable
// connectorProfileArn, a derived credentialsArn, and create/update timestamps.
// A profile name already in use yields a ConflictException.
func (m *Mock) CreateConnectorProfile(_ context.Context,
	in *driver.CreateConnectorProfileInput) (*driver.ConnectorProfile, error) {
	if in.ConnectorProfileName == "" {
		return nil, validation("connectorProfileName is required")
	}

	if in.ConnectorType == "" {
		return nil, validation("connectorType is required")
	}

	if m.profiles.Has(in.ConnectorProfileName) {
		return nil, conflict("connector profile %s already exists", in.ConnectorProfileName)
	}

	now := m.now()

	profile := driver.ConnectorProfile{
		ConnectorProfileName: in.ConnectorProfileName,
		ConnectorProfileArn:  m.connectorProfileARN(in.ConnectorProfileName),
		ConnectorType:        in.ConnectorType,
		ConnectorLabel:       in.ConnectorLabel,
		ConnectionMode:       in.ConnectionMode,
		CredentialsArn:       m.credentialsARN(in.ConnectorProfileName),
		CreatedAt:            now,
		LastUpdatedAt:        now,
		Extra:                copyExtra(in.Extra),
	}

	m.profiles.Set(in.ConnectorProfileName, profile)

	out := copyProfile(&profile)

	return &out, nil
}

// UpdateConnectorProfile replaces the mutable configuration of a connector
// profile while preserving its computed arn, credentialsArn, connectorType, and
// createdAt. It bumps lastUpdatedAt.
func (m *Mock) UpdateConnectorProfile(_ context.Context,
	in *driver.UpdateConnectorProfileInput) (*driver.ConnectorProfile, error) {
	var updated driver.ConnectorProfile

	ok := m.profiles.Update(in.ConnectorProfileName, func(p driver.ConnectorProfile) driver.ConnectorProfile {
		p.Extra = copyExtra(in.Extra)
		p.LastUpdatedAt = m.now()
		updated = p

		return p
	})
	if !ok {
		return nil, notFound("connector profile %s not found", in.ConnectorProfileName)
	}

	out := copyProfile(&updated)

	return &out, nil
}

// DeleteConnectorProfile removes a connector profile. forceDelete is accepted
// for wire compatibility; the emulator deletes unconditionally.
func (m *Mock) DeleteConnectorProfile(_ context.Context, name string, _ bool) error {
	if !m.profiles.Delete(name) {
		return notFound("connector profile %s not found", name)
	}

	return nil
}

// DescribeConnectorProfiles returns a deterministic, deep-copied page of the
// connector profiles, optionally filtered by name and/or connector type.
func (m *Mock) DescribeConnectorProfiles(_ context.Context, names []string, connectorType string,
	page driver.Page) ([]driver.ConnectorProfile, string, error) {
	wanted := make(map[string]bool, len(names))
	for _, n := range names {
		wanted[n] = true
	}

	stored := m.profiles.SortedValues()

	all := make([]driver.ConnectorProfile, 0, len(stored))

	for i := range stored {
		p := &stored[i]
		if len(wanted) > 0 && !wanted[p.ConnectorProfileName] {
			continue
		}

		if connectorType != "" && p.ConnectorType != connectorType {
			continue
		}

		all = append(all, copyProfile(p))
	}

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}
