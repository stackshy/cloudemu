package azuresql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// dbKey is the storage key for a logical database: "server/name".
func dbKey(server, name string) string { return server + "/" + name }

// CreateDatabase creates a logical database on a SQL server, implementing the
// relationaldb Databases optional capability. SKU/tier default to the common
// General Purpose Gen5 shape a discoverer can price when the request omits them.
//
//nolint:gocritic // cfg matches the Databases capability interface signature.
func (m *Mock) CreateDatabase(_ context.Context, cfg rdsdriver.DatabaseConfig) (*rdsdriver.Database, error) {
	if cfg.Server == "" || cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "server and database name are required")
	}

	key := dbKey(cfg.Server, cfg.Name)
	if _, ok := m.databases.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "database %q already exists on server %q", cfg.Name, cfg.Server)
	}

	skuName := cfg.SKUName
	if skuName == "" {
		skuName = "GP_Gen5_2"
	}

	skuTier := cfg.SKUTier
	if skuTier == "" {
		skuTier = "GeneralPurpose"
	}

	db := rdsdriver.Database{
		Server:        cfg.Server,
		Name:          cfg.Name,
		Charset:       cfg.Charset,
		Collation:     cfg.Collation,
		ARN:           serverDatabaseResourceID(m.opts.Region, cfg.Server, cfg.Name),
		SKUName:       skuName,
		SKUTier:       skuTier,
		ZoneRedundant: cfg.ZoneRedundant,
	}
	m.databases.Set(key, db)

	out := db

	return &out, nil
}

// GetDatabase returns a logical database, or NotFound.
func (m *Mock) GetDatabase(_ context.Context, server, name string) (*rdsdriver.Database, error) {
	db, ok := m.databases.Get(dbKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", name, server)
	}

	out := db

	return &out, nil
}

// ListDatabases returns every logical database on a server.
func (m *Mock) ListDatabases(_ context.Context, server string) ([]rdsdriver.Database, error) {
	out := []rdsdriver.Database{}

	for _, db := range m.databases.SortedValues() {
		if db.Server == server {
			out = append(out, db)
		}
	}

	return out, nil
}

// DeleteDatabase removes a logical database, or returns NotFound.
func (m *Mock) DeleteDatabase(_ context.Context, server, name string) error {
	if !m.databases.Delete(dbKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", name, server)
	}

	return nil
}
