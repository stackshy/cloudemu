package sql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// SetTransparentDataEncryption sets the TDE state of a logical database,
// implementing the relationaldb TransparentDataEncryptions optional capability.
// It is the create-or-update for the transparentDataEncryption/current
// sub-resource: the database must exist (real Azure answers 404 otherwise), and
// an unset state defaults to Enabled to match Azure's encrypted-at-rest default.
func (m *Mock) SetTransparentDataEncryption(
	_ context.Context, cfg rdsdriver.TransparentDataEncryptionConfig,
) (*rdsdriver.TransparentDataEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := dbKey(cfg.Server, cfg.Database)
	if _, ok := m.databases.Get(key); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", cfg.Database, cfg.Server)
	}

	state := cfg.State
	if state == "" {
		state = rdsdriver.TDEStateEnabled
	}

	rec := rdsdriver.TransparentDataEncryption{Server: cfg.Server, Database: cfg.Database, State: state}
	m.tde.Set(key, rec)

	out := rec

	return &out, nil
}

// GetTransparentDataEncryption returns a database's TDE state, or NotFound.
func (m *Mock) GetTransparentDataEncryption(
	_ context.Context, server, database string,
) (*rdsdriver.TransparentDataEncryption, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := m.tde.Get(dbKey(server, database))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", database, server)
	}

	out := rec

	return &out, nil
}

// ListTransparentDataEncryption returns a database's TDE records. Azure models
// TDE as the single "current" sub-resource, so the list holds one entry (or is
// empty when the database does not exist).
func (m *Mock) ListTransparentDataEncryption(
	_ context.Context, server, database string,
) ([]rdsdriver.TransparentDataEncryption, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := []rdsdriver.TransparentDataEncryption{}
	if rec, ok := m.tde.Get(dbKey(server, database)); ok {
		out = append(out, rec)
	}

	return out, nil
}
