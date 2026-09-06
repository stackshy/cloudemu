package spanner

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

func cloneDatabase(in *spdriver.Database) spdriver.Database {
	d := *in
	d.DDL = append([]string(nil), in.DDL...)

	return d
}

// CreateDatabase creates a database under an existing instance, ready
// immediately. The initial DDL is the request's extra statements.
func (m *Mock) CreateDatabase(_ context.Context, cfg spdriver.CreateDatabaseConfig) (*spdriver.Database, *spdriver.Operation, error) {
	if cfg.Name == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "database name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(cfg.Parent) {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", cfg.Parent)
	}

	if m.databases.Has(cfg.Name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "database %q already exists", cfg.Name)
	}

	db := spdriver.Database{
		Name:                   cfg.Name,
		State:                  spdriver.StateReady,
		DatabaseDialect:        orDefault(cfg.DatabaseDialect, spdriver.DialectGoogleStandardSQL),
		VersionRetentionPeriod: "1h",
		DDL:                    append([]string(nil), cfg.ExtraStatements...),
		CreateTime:             m.opts.Clock.Now().UTC(),
	}
	m.databases.Set(cfg.Name, db)

	op := m.newOp(cfg.Name, "create-database", cfg.Name)
	out := cloneDatabase(&db)

	return &out, op, nil
}

// GetDatabase returns a database by full name.
func (m *Mock) GetDatabase(_ context.Context, name string) (*spdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, ok := m.databases.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	out := cloneDatabase(&db)

	return &out, nil
}

// ListDatabases returns every database under an instance (parent).
func (m *Mock) ListDatabases(_ context.Context, parent string) ([]spdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := parent + "/databases/"
	all := m.databases.SortedValues()
	out := make([]spdriver.Database, 0, len(all))

	for i := range all {
		if strings.HasPrefix(all[i].Name, prefix) {
			out = append(out, cloneDatabase(&all[i]))
		}
	}

	return out, nil
}

// UpdateDatabaseDdl appends DDL statements to a database and returns the LRO.
func (m *Mock) UpdateDatabaseDdl(
	_ context.Context, name string, statements []string,
) (*spdriver.Database, *spdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	db, ok := m.databases.Get(name)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	db.DDL = append(db.DDL, statements...)
	m.databases.Set(name, db)

	op := m.newOp(name, "update-ddl", name)
	out := cloneDatabase(&db)

	return &out, op, nil
}

// GetDatabaseDdl returns a database's accumulated DDL statements.
func (m *Mock) GetDatabaseDdl(_ context.Context, name string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, ok := m.databases.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	return append([]string(nil), db.DDL...), nil
}

// DropDatabase deletes a database (synchronous).
func (m *Mock) DropDatabase(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.databases.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	m.databases.Delete(name)

	return nil
}
