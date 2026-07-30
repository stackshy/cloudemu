package alloydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// AlloyDB users and databases are cluster-scoped. These satisfy the optional
// Users and Databases relationaldb capabilities; the "instance"/"server"
// argument is the AlloyDB cluster name.
var (
	_ rdsdriver.Users     = (*Mock)(nil)
	_ rdsdriver.Databases = (*Mock)(nil)
)

func childKey(cluster, name string) string { return cluster + "/" + name }

func (m *Mock) requireCluster(cluster string) error {
	if !m.clusters.Has(cluster) {
		return cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", cluster)
	}

	return nil
}

// ---- Users ----

// CreateUser adds a database user to a cluster.
func (m *Mock) CreateUser(_ context.Context, cfg rdsdriver.UserConfig) (*rdsdriver.User, error) {
	if err := validName("user", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireCluster(cfg.Instance); err != nil {
		return nil, err
	}

	key := childKey(cfg.Instance, cfg.Name)
	if _, ok := m.users.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB user %q already exists", cfg.Name)
	}

	user := rdsdriver.User{
		Instance: cfg.Instance,
		Name:     cfg.Name,
		Host:     cfg.Host,
	}

	m.users.Set(key, user)

	out := user

	return &out, nil
}

// GetUser returns a single user.
func (m *Mock) GetUser(_ context.Context, cluster, name string) (*rdsdriver.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users.Get(childKey(cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB user %q not found", name)
	}

	out := user

	return &out, nil
}

// ListUsers returns all users in a cluster.
func (m *Mock) ListUsers(_ context.Context, cluster string) ([]rdsdriver.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireCluster(cluster); err != nil {
		return nil, err
	}

	out := []rdsdriver.User{}

	for _, u := range m.users.SortedValues() {
		if u.Instance == cluster {
			out = append(out, u)
		}
	}

	return out, nil
}

// UpdateUser updates an existing user (host/roles; password is input-only).
func (m *Mock) UpdateUser(_ context.Context, cfg rdsdriver.UserConfig) (*rdsdriver.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := childKey(cfg.Instance, cfg.Name)

	user, ok := m.users.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB user %q not found", cfg.Name)
	}

	if cfg.Host != "" {
		user.Host = cfg.Host
	}

	m.users.Set(key, user)

	out := user

	return &out, nil
}

// DeleteUser removes a user.
func (m *Mock) DeleteUser(_ context.Context, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.users.Delete(childKey(cluster, name)) {
		return cerrors.Newf(cerrors.NotFound, "AlloyDB user %q not found", name)
	}

	return nil
}

// ---- Databases ----

// CreateDatabase adds a logical database to a cluster.
func (m *Mock) CreateDatabase(_ context.Context, cfg rdsdriver.DatabaseConfig) (*rdsdriver.Database, error) {
	if err := validName("database", cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireCluster(cfg.Server); err != nil {
		return nil, err
	}

	key := childKey(cfg.Server, cfg.Name)
	if _, ok := m.databases.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "AlloyDB database %q already exists", cfg.Name)
	}

	charset := cfg.Charset
	if charset == "" {
		charset = "UTF8"
	}

	db := rdsdriver.Database{
		Server:    cfg.Server,
		Name:      cfg.Name,
		Charset:   charset,
		Collation: cfg.Collation,
		ARN:       m.clusterName(cfg.Server) + "/databases/" + cfg.Name,
	}

	m.databases.Set(key, db)

	out := db

	return &out, nil
}

// GetDatabase returns a single database.
func (m *Mock) GetDatabase(_ context.Context, cluster, name string) (*rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, ok := m.databases.Get(childKey(cluster, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB database %q not found", name)
	}

	out := db

	return &out, nil
}

// ListDatabases returns all databases in a cluster.
func (m *Mock) ListDatabases(_ context.Context, cluster string) ([]rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireCluster(cluster); err != nil {
		return nil, err
	}

	out := []rdsdriver.Database{}

	for _, db := range m.databases.SortedValues() {
		if db.Server == cluster {
			out = append(out, db)
		}
	}

	return out, nil
}

// DeleteDatabase removes a database.
func (m *Mock) DeleteDatabase(_ context.Context, cluster, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.databases.Delete(childKey(cluster, name)) {
		return cerrors.Newf(cerrors.NotFound, "AlloyDB database %q not found", name)
	}

	return nil
}
