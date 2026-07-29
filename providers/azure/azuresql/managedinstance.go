package azuresql

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Azure SQL Managed Instance is a distinct Microsoft.Sql resource type from the
// single-database logical server; it hosts managed databases. Modeled as an
// optional relationaldb capability discovered by type assertion.
var _ rdsdriver.ManagedInstances = (*Mock)(nil)

const (
	miDefaultSKU     = "GP_Gen5"
	miDefaultTier    = "GeneralPurpose"
	miDefaultVCores  = 4
	miDefaultStorage = 32
)

func (m *Mock) miARN(name string) string {
	return idgen.AzureID(m.opts.Region, m.opts.Region, armProvider, "managedInstances", name)
}

func (m *Mock) mdbARN(instance, name string) string {
	return idgen.AzureID(m.opts.Region, m.opts.Region, armProvider, "managedInstances/"+instance+"/databases", name)
}

// CreateManagedInstance provisions a SQL Managed Instance.
//
//nolint:gocritic // cfg matches the ManagedInstances capability interface signature.
func (m *Mock) CreateManagedInstance(
	_ context.Context, cfg rdsdriver.ManagedInstanceConfig,
) (*rdsdriver.ManagedInstance, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "managed instance name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.managedInstances.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "managed instance %q already exists", cfg.Name)
	}

	mi := rdsdriver.ManagedInstance{
		Name:        cfg.Name,
		Location:    orDefault(cfg.Location, m.opts.Region),
		AdminLogin:  cfg.AdminLogin,
		SKUName:     orDefault(cfg.SKUName, miDefaultSKU),
		SKUTier:     orDefault(cfg.SKUTier, miDefaultTier),
		LicenseType: orDefault(cfg.LicenseType, "LicenseIncluded"),
		SubnetID:    cfg.SubnetID,
		VCores:      orDefaultInt(cfg.VCores, miDefaultVCores),
		StorageGB:   orDefaultInt(cfg.StorageGB, miDefaultStorage),
		State:       "Ready",
		FQDN:        cfg.Name + ".managed.database.windows.net",
		ARN:         m.miARN(cfg.Name),
		Tags:        copyTags(cfg.Tags),
	}

	m.managedInstances.Set(cfg.Name, mi)

	out := mi

	return &out, nil
}

// GetManagedInstance returns a managed instance by name.
func (m *Mock) GetManagedInstance(_ context.Context, name string) (*rdsdriver.ManagedInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mi, ok := m.managedInstances.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed instance %q not found", name)
	}

	out := mi

	return &out, nil
}

// ListManagedInstances returns all managed instances.
func (m *Mock) ListManagedInstances(_ context.Context) ([]rdsdriver.ManagedInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := []rdsdriver.ManagedInstance{}

	//nolint:gocritic // map values materialized into the result slice.
	for _, mi := range m.managedInstances.All() {
		out = append(out, mi)
	}

	return out, nil
}

// DeleteManagedInstance removes a managed instance and cascades to its managed
// databases.
func (m *Mock) DeleteManagedInstance(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.managedInstances.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "managed instance %q not found", name)
	}

	prefix := name + "/"
	for key := range m.managedDatabases.All() {
		if strings.HasPrefix(key, prefix) {
			m.managedDatabases.Delete(key)
		}
	}

	return nil
}

// StartManagedInstance marks a managed instance ready.
func (m *Mock) StartManagedInstance(ctx context.Context, name string) error {
	return m.setManagedInstanceState(ctx, name, "Ready")
}

// StopManagedInstance marks a managed instance stopped.
func (m *Mock) StopManagedInstance(ctx context.Context, name string) error {
	return m.setManagedInstanceState(ctx, name, "Stopped")
}

// FailoverManagedInstance triggers a managed-instance failover; the instance
// stays ready.
func (m *Mock) FailoverManagedInstance(ctx context.Context, name string) error {
	return m.setManagedInstanceState(ctx, name, "Ready")
}

func (m *Mock) setManagedInstanceState(_ context.Context, name, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mi, ok := m.managedInstances.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "managed instance %q not found", name)
	}

	mi.State = state
	m.managedInstances.Set(name, mi)

	return nil
}

// CreateManagedDatabase adds a database to a managed instance.
func (m *Mock) CreateManagedDatabase(
	_ context.Context, cfg rdsdriver.ManagedDatabaseConfig,
) (*rdsdriver.ManagedDatabase, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "managed database name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.managedInstances.Get(cfg.Instance); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed instance %q not found", cfg.Instance)
	}

	key := subKey(cfg.Instance, cfg.Name)
	if _, ok := m.managedDatabases.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "managed database %q already exists", cfg.Name)
	}

	mdb := rdsdriver.ManagedDatabase{
		Instance:  cfg.Instance,
		Name:      cfg.Name,
		Collation: orDefault(cfg.Collation, "SQL_Latin1_General_CP1_CI_AS"),
		Status:    "Online",
		ARN:       m.mdbARN(cfg.Instance, cfg.Name),
	}

	m.managedDatabases.Set(key, mdb)

	out := mdb

	return &out, nil
}

// GetManagedDatabase returns a managed database.
func (m *Mock) GetManagedDatabase(_ context.Context, instance, name string) (*rdsdriver.ManagedDatabase, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mdb, ok := m.managedDatabases.Get(subKey(instance, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed database %q not found", name)
	}

	out := mdb

	return &out, nil
}

// ListManagedDatabases returns all databases on a managed instance.
func (m *Mock) ListManagedDatabases(_ context.Context, instance string) ([]rdsdriver.ManagedDatabase, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.managedInstances.Get(instance); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed instance %q not found", instance)
	}

	out := []rdsdriver.ManagedDatabase{}

	for _, mdb := range m.managedDatabases.All() {
		if mdb.Instance == instance {
			out = append(out, mdb)
		}
	}

	return out, nil
}

// DeleteManagedDatabase removes a managed database.
func (m *Mock) DeleteManagedDatabase(_ context.Context, instance, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.managedDatabases.Delete(subKey(instance, name)) {
		return cerrors.Newf(cerrors.NotFound, "managed database %q not found", name)
	}

	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}

	return v
}
