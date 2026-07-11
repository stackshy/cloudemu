package azuresearch

import (
	"context"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/errors"
)

// svcChildID builds the ARM ID for a resource nested under a search service.
func (m *Mock) svcChildID(resourceGroup, service, childPath string) string {
	if s, ok := m.services.Get(key(resourceGroup, service)); ok {
		return s.ID + "/" + childPath
	}

	return ""
}

// --- Admin & query keys ---

// currentAdminKeys returns the persisted admin keys, or the deterministic
// defaults when the service has never had a key regenerated.
func (m *Mock) currentAdminKeys(resourceGroup, name string) *driver.AdminKeys {
	if k, ok := m.adminKeys.Get(key(resourceGroup, name)); ok {
		out := *k

		return &out
	}

	return &driver.AdminKeys{
		Primary:   hashHex(resourceGroup, name, "admin-primary"),
		Secondary: hashHex(resourceGroup, name, "admin-secondary"),
	}
}

func (m *Mock) ListAdminKeys(_ context.Context, resourceGroup, name string) (*driver.AdminKeys, error) {
	if err := m.requireService(resourceGroup, name); err != nil {
		return nil, err
	}

	return m.currentAdminKeys(resourceGroup, name), nil
}

func (m *Mock) RegenerateAdminKey(_ context.Context, resourceGroup, name, which string) (*driver.AdminKeys, error) {
	if err := m.requireService(resourceGroup, name); err != nil {
		return nil, err
	}

	keys := m.currentAdminKeys(resourceGroup, name)

	// Salt with a monotonic counter so each regeneration yields a distinct key
	// and the rotation is observable via a subsequent ListAdminKeys.
	salt := "regen-" + strconv.FormatInt(m.seq.Add(1), 16)
	if strings.EqualFold(which, "secondary") {
		keys.Secondary = hashHex(resourceGroup, name, "admin-secondary", salt)
	} else {
		keys.Primary = hashHex(resourceGroup, name, "admin-primary", salt)
	}

	m.adminKeys.Set(key(resourceGroup, name), keys)

	out := *keys

	return &out, nil
}

func (m *Mock) ListQueryKeys(_ context.Context, resourceGroup, name string) ([]driver.QueryKey, error) {
	if err := m.requireService(resourceGroup, name); err != nil {
		return nil, err
	}

	prefix := key(resourceGroup, name) + "/"
	out := []driver.QueryKey{{Name: "", Key: hashHex(resourceGroup, name, "query-default")}}

	for k, qk := range m.queryKeys.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *qk)
		}
	}

	return out, nil
}

func (m *Mock) CreateQueryKey(_ context.Context, resourceGroup, name, keyName string) (*driver.QueryKey, error) {
	if err := m.requireService(resourceGroup, name); err != nil {
		return nil, err
	}

	qk := &driver.QueryKey{Name: keyName, Key: hashHex(resourceGroup, name, "query", keyName)}
	m.queryKeys.Set(key(resourceGroup, name, qk.Key), qk)

	out := *qk

	return &out, nil
}

func (m *Mock) DeleteQueryKey(_ context.Context, resourceGroup, name, queryKey string) error {
	if err := m.requireService(resourceGroup, name); err != nil {
		return err
	}

	if !m.queryKeys.Delete(key(resourceGroup, name, queryKey)) {
		return errors.Newf(errors.NotFound, "query key not found")
	}

	return nil
}

// --- Shared private links ---

func (m *Mock) PutSharedPrivateLink(
	_ context.Context, resourceGroup, name, linkName, groupID, privateLinkID string,
) (*driver.SharedPrivateLink, error) {
	if err := m.requireService(resourceGroup, name); err != nil {
		return nil, err
	}

	l := &driver.SharedPrivateLink{
		ID:                m.svcChildID(resourceGroup, name, "sharedPrivateLinkResources/"+linkName),
		Name:              linkName,
		GroupID:           groupID,
		PrivateLinkID:     privateLinkID,
		Status:            "Pending",
		ProvisioningState: driver.StateSucceeded,
	}
	m.sharedLinks.Set(key(resourceGroup, name, "spl", linkName), l)

	out := *l

	return &out, nil
}

func (m *Mock) GetSharedPrivateLink(_ context.Context, resourceGroup, name, linkName string) (*driver.SharedPrivateLink, error) {
	l, ok := m.sharedLinks.Get(key(resourceGroup, name, "spl", linkName))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "shared private link %q not found", linkName)
	}

	out := *l

	return &out, nil
}

func (m *Mock) DeleteSharedPrivateLink(_ context.Context, resourceGroup, name, linkName string) error {
	if !m.sharedLinks.Delete(key(resourceGroup, name, "spl", linkName)) {
		return errors.Newf(errors.NotFound, "shared private link %q not found", linkName)
	}

	return nil
}

func (m *Mock) ListSharedPrivateLinks(_ context.Context, resourceGroup, name string) ([]driver.SharedPrivateLink, error) {
	prefix := key(resourceGroup, name, "spl") + "/"
	out := make([]driver.SharedPrivateLink, 0)

	for k, l := range m.sharedLinks.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *l)
		}
	}

	return out, nil
}

// --- Private endpoint connections ---

func (m *Mock) PutPrivateEndpointConnection(
	_ context.Context, resourceGroup, name, connName, status string,
) (*driver.PrivateEndpointConnection, error) {
	if err := m.requireService(resourceGroup, name); err != nil {
		return nil, err
	}

	if status == "" {
		status = "Approved"
	}

	c := &driver.PrivateEndpointConnection{
		ID:                m.svcChildID(resourceGroup, name, "privateEndpointConnections/"+connName),
		Name:              connName,
		Status:            status,
		ProvisioningState: driver.StateSucceeded,
	}
	m.privateConns.Set(key(resourceGroup, name, "pec", connName), c)

	out := *c

	return &out, nil
}

func (m *Mock) GetPrivateEndpointConnection(
	_ context.Context, resourceGroup, name, connName string,
) (*driver.PrivateEndpointConnection, error) {
	c, ok := m.privateConns.Get(key(resourceGroup, name, "pec", connName))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "private endpoint connection %q not found", connName)
	}

	out := *c

	return &out, nil
}

func (m *Mock) DeletePrivateEndpointConnection(_ context.Context, resourceGroup, name, connName string) error {
	if !m.privateConns.Delete(key(resourceGroup, name, "pec", connName)) {
		return errors.Newf(errors.NotFound, "private endpoint connection %q not found", connName)
	}

	return nil
}

func (m *Mock) ListPrivateEndpointConnections(
	_ context.Context, resourceGroup, name string,
) ([]driver.PrivateEndpointConnection, error) {
	prefix := key(resourceGroup, name, "pec") + "/"
	out := make([]driver.PrivateEndpointConnection, 0)

	for k, c := range m.privateConns.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *c)
		}
	}

	return out, nil
}
