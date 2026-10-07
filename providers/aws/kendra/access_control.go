package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

const maxAccessControlName = 200

func (m *Mock) accessControlChild() *child[driver.AccessControlConfiguration] {
	return &child[driver.AccessControlConfiguration]{
		kind: "access control configuration", store: m.accessControls,
		idOf:    func(a *driver.AccessControlConfiguration) string { return a.ID },
		indexOf: func(a *driver.AccessControlConfiguration) string { return a.IndexID },
		tokenOf: func(a *driver.AccessControlConfiguration) string { return a.ClientToken },
	}
}

func copyAccessControl(a *driver.AccessControlConfiguration) driver.AccessControlConfiguration {
	out := *a
	out.AccessControlList = copyRaw(a.AccessControlList)
	out.HierarchicalAccessControlList = copyRaw(a.HierarchicalAccessControlList)

	return out
}

// CreateAccessControlConfiguration stores a reusable access control list for an
// index's documents. A repeated ClientToken returns the first configuration.
func (m *Mock) CreateAccessControlConfiguration(
	_ context.Context, in *driver.CreateAccessControlInput,
) (*driver.AccessControlConfiguration, error) {
	if err := validateIndexID(in.IndexID); err != nil {
		return nil, err
	}

	if err := validateAccessControlName(in.Name); err != nil {
		return nil, err
	}

	if err := validateCommon(in.ClientToken, in.Description, nil); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	if existing, ok := childByToken(m.accessControlChild(), in.IndexID, in.ClientToken); ok {
		out := copyAccessControl(&existing)

		return &out, nil
	}

	id := newUUID()
	a := driver.AccessControlConfiguration{
		ID: id, IndexID: in.IndexID, Name: in.Name, Description: in.Description,
		AccessControlList:             copyRaw(in.AccessControlList),
		HierarchicalAccessControlList: copyRaw(in.HierarchicalAccessControlList),
		ClientToken:                   in.ClientToken,
	}

	m.accessControls.Set(childKey(in.IndexID, id), a)

	out := copyAccessControl(&a)

	return &out, nil
}

// DescribeAccessControlConfiguration returns a configuration by index and id.
func (m *Mock) DescribeAccessControlConfiguration(
	_ context.Context, indexID, id string,
) (*driver.AccessControlConfiguration, error) {
	a, err := childGet(m, m.accessControlChild(), indexID, id)
	if err != nil {
		return nil, err
	}

	out := copyAccessControl(&a)

	return &out, nil
}

// UpdateAccessControlConfiguration applies the supplied fields, leaving omitted
// ones unchanged.
func (m *Mock) UpdateAccessControlConfiguration(_ context.Context, in *driver.UpdateAccessControlInput) error {
	if in.Name != nil {
		if err := validateAccessControlName(*in.Name); err != nil {
			return err
		}
	}

	if in.Description != nil && len(*in.Description) > maxDescriptionLen {
		return validation("Description must have length between 0 and %d", maxDescriptionLen)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childGet(m, m.accessControlChild(), in.IndexID, in.ID); err != nil {
		return err
	}

	m.accessControls.Update(childKey(in.IndexID, in.ID), func(a driver.AccessControlConfiguration) driver.AccessControlConfiguration {
		applyAccessControlUpdate(&a, in)

		return a
	})

	return nil
}

func applyAccessControlUpdate(a *driver.AccessControlConfiguration, in *driver.UpdateAccessControlInput) {
	if in.Name != nil {
		a.Name = *in.Name
	}

	if in.Description != nil {
		a.Description = *in.Description
	}

	if in.AccessControlList != nil {
		a.AccessControlList = copyRaw(in.AccessControlList)
	}

	if in.HierarchicalAccessControlList != nil {
		a.HierarchicalAccessControlList = copyRaw(in.HierarchicalAccessControlList)
	}
}

// validateAccessControlName checks the 1..200 length and the name pattern.
func validateAccessControlName(name string) error {
	if len(name) < 1 || len(name) > maxAccessControlName || !namePatternRE.MatchString(name) {
		return validation("Name must have length between 1 and %d and match [a-zA-Z0-9][a-zA-Z0-9_-]*", maxAccessControlName)
	}

	return nil
}

// ListAccessControlConfigurations returns a deterministic page of an index's
// access control configurations ordered by id.
func (m *Mock) ListAccessControlConfigurations(
	_ context.Context, indexID string, page driver.Page,
) (configs []driver.AccessControlConfiguration, nextToken string, err error) {
	items, next, err := childList(m, m.accessControlChild(), indexID, page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]driver.AccessControlConfiguration, 0, len(items))
	for i := range items {
		out = append(out, copyAccessControl(&items[i]))
	}

	return out, next, nil
}

// DeleteAccessControlConfiguration removes a configuration.
func (m *Mock) DeleteAccessControlConfiguration(_ context.Context, indexID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childGet(m, m.accessControlChild(), indexID, id); err != nil {
		return err
	}

	m.accessControls.Delete(childKey(indexID, id))

	return nil
}
