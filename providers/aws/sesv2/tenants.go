package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// CreateTenant registers a sending tenant.
func (m *Mock) CreateTenant(_ context.Context, name string, tags map[string]string) (*driver.Tenant, error) {
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "TenantName is required")
	}

	id := idgen.GenerateID("")
	t := driver.Tenant{
		Name:          name,
		ID:            id,
		ARN:           idgen.AWSARN("ses", m.opts.Region, m.opts.AccountID, "tenant/"+name),
		CreatedAt:     m.now(),
		SendingStatus: driver.SendingStatusEnabled,
		Tags:          copyTags(tags),
	}

	data := &tenantData{t: t, resources: memstore.New[driver.TenantResource]()}
	if !m.tenants.SetIfAbsent(name, data) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "tenant %q already exists", name)
	}

	out := t

	return &out, nil
}

// GetTenant returns a tenant by name.
func (m *Mock) GetTenant(_ context.Context, name string) (*driver.Tenant, error) {
	d, err := m.getTenant(name)
	if err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := d.t
	out.Tags = copyTags(d.t.Tags)

	return &out, nil
}

// DeleteTenant removes a tenant.
func (m *Mock) DeleteTenant(_ context.Context, name string) error {
	if !m.tenants.Delete(name) {
		return errTenantNotFound(name)
	}

	return nil
}

// ListTenants returns all tenants ordered by name.
func (m *Mock) ListTenants(_ context.Context) ([]driver.Tenant, error) {
	all := m.tenants.SortedValues()
	out := make([]driver.Tenant, 0, len(all))

	for _, d := range all {
		d.mu.RLock()
		out = append(out, d.t)
		d.mu.RUnlock()
	}

	return out, nil
}

// CreateTenantResourceAssociation links a resource to a tenant.
func (m *Mock) CreateTenantResourceAssociation(_ context.Context, tenantName, resourceARN string) error {
	d, err := m.getTenant(tenantName)
	if err != nil {
		return err
	}

	d.resources.Set(resourceARN, driver.TenantResource{
		TenantName:  tenantName,
		ResourceARN: resourceARN,
	})

	return nil
}

// DeleteTenantResourceAssociation unlinks a resource from a tenant.
func (m *Mock) DeleteTenantResourceAssociation(_ context.Context, tenantName, resourceARN string) error {
	d, err := m.getTenant(tenantName)
	if err != nil {
		return err
	}

	if !d.resources.Delete(resourceARN) {
		return cerrors.Newf(cerrors.NotFound, "resource %q is not associated with tenant %q", resourceARN, tenantName)
	}

	return nil
}

// ListTenantResources lists a tenant's associated resources.
func (m *Mock) ListTenantResources(_ context.Context, tenantName string) ([]driver.TenantResource, error) {
	d, err := m.getTenant(tenantName)
	if err != nil {
		return nil, err
	}

	return d.resources.SortedValues(), nil
}

// ListResourceTenants lists tenants a resource is associated with.
func (m *Mock) ListResourceTenants(_ context.Context, resourceARN string) ([]driver.Tenant, error) {
	all := m.tenants.SortedValues()
	out := make([]driver.Tenant, 0, len(all))

	for _, d := range all {
		if d.resources.Has(resourceARN) {
			d.mu.RLock()
			out = append(out, d.t)
			d.mu.RUnlock()
		}
	}

	return out, nil
}

// PutTenantSuppressionAttributes accepts a tenant's suppressed reasons. The
// value is validated against an existing tenant but not otherwise modeled.
func (m *Mock) PutTenantSuppressionAttributes(_ context.Context, tenantName string, _ []string) error {
	if _, err := m.getTenant(tenantName); err != nil {
		return err
	}

	return nil
}

func (m *Mock) getTenant(name string) (*tenantData, error) {
	d, ok := m.tenants.Get(name)
	if !ok {
		return nil, errTenantNotFound(name)
	}

	return d, nil
}

func errTenantNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "tenant %q does not exist", name)
}
