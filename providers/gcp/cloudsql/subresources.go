package cloudsql

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 fingerprints are the Cloud SQL cert identifier format, not a security control.
	"encoding/hex"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Cloud SQL exposes databases, users and SSL certs as instance child resources,
// and supports clone, failover and replica lifecycle actions on instances.
// These are optional relationaldb driver capabilities discovered by the REST
// handler via type assertion.
var (
	_ rdsdriver.Databases        = (*Mock)(nil)
	_ rdsdriver.Users            = (*Mock)(nil)
	_ rdsdriver.SslCerts         = (*Mock)(nil)
	_ rdsdriver.Failover         = (*Mock)(nil)
	_ rdsdriver.Clonable         = (*Mock)(nil)
	_ rdsdriver.ReplicaPromotion = (*Mock)(nil)
)

const (
	defaultCharset   = "UTF8"
	defaultCollation = "en_US.UTF8"
	defaultUserHost  = "%"
)

func childKey(instance, name string) string { return instance + "/" + name }

func (m *Mock) requireInstance(instance string) error {
	if _, ok := m.instances.Get(instance); !ok {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", instance)
	}

	return nil
}

// ---- Databases ----

// CreateDatabase adds a logical database to an instance.
func (m *Mock) CreateDatabase(_ context.Context, cfg rdsdriver.DatabaseConfig) (*rdsdriver.Database, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "database name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireInstance(cfg.Server); err != nil {
		return nil, err
	}

	key := childKey(cfg.Server, cfg.Name)
	if _, ok := m.databases.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "database %q already exists", cfg.Name)
	}

	charset := cfg.Charset
	if charset == "" {
		charset = defaultCharset
	}

	collation := cfg.Collation
	if collation == "" {
		collation = defaultCollation
	}

	db := rdsdriver.Database{
		Server:    cfg.Server,
		Name:      cfg.Name,
		Charset:   charset,
		Collation: collation,
		ARN:       idgen.GCPID(m.opts.ProjectID, "instances/"+cfg.Server+"/databases", cfg.Name),
	}

	m.databases.Set(key, db)

	out := db

	return &out, nil
}

// GetDatabase returns a single logical database.
func (m *Mock) GetDatabase(_ context.Context, instance, name string) (*rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, ok := m.databases.Get(childKey(instance, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	out := db

	return &out, nil
}

// ListDatabases returns all logical databases in an instance.
func (m *Mock) ListDatabases(_ context.Context, instance string) ([]rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireInstance(instance); err != nil {
		return nil, err
	}

	out := []rdsdriver.Database{}

	for _, db := range m.databases.All() {
		if db.Server == instance {
			out = append(out, db)
		}
	}

	return out, nil
}

// DeleteDatabase removes a logical database.
func (m *Mock) DeleteDatabase(_ context.Context, instance, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.databases.Delete(childKey(instance, name)) {
		return cerrors.Newf(cerrors.NotFound, "database %q not found", name)
	}

	return nil
}

// ---- Users ----

// CreateUser adds a database user to an instance.
func (m *Mock) CreateUser(_ context.Context, cfg rdsdriver.UserConfig) (*rdsdriver.User, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "user name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireInstance(cfg.Instance); err != nil {
		return nil, err
	}

	key := childKey(cfg.Instance, cfg.Name)
	if _, ok := m.users.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "user %q already exists", cfg.Name)
	}

	host := cfg.Host
	if host == "" {
		host = defaultUserHost
	}

	user := rdsdriver.User{Instance: cfg.Instance, Name: cfg.Name, Host: host}
	m.users.Set(key, user)

	out := user

	return &out, nil
}

// GetUser returns a single database user.
func (m *Mock) GetUser(_ context.Context, instance, name string) (*rdsdriver.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users.Get(childKey(instance, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", name)
	}

	out := user

	return &out, nil
}

// ListUsers returns all users in an instance.
func (m *Mock) ListUsers(_ context.Context, instance string) ([]rdsdriver.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireInstance(instance); err != nil {
		return nil, err
	}

	out := []rdsdriver.User{}

	for _, user := range m.users.All() {
		if user.Instance == instance {
			out = append(out, user)
		}
	}

	return out, nil
}

// UpdateUser updates an existing user (host is the only mutable field the mock
// tracks). Cloud SQL's Update is idempotent create-or-update.
func (m *Mock) UpdateUser(_ context.Context, cfg rdsdriver.UserConfig) (*rdsdriver.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireInstance(cfg.Instance); err != nil {
		return nil, err
	}

	key := childKey(cfg.Instance, cfg.Name)

	user, ok := m.users.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "user %q not found", cfg.Name)
	}

	if cfg.Host != "" {
		user.Host = cfg.Host
	}

	m.users.Set(key, user)

	out := user

	return &out, nil
}

// DeleteUser removes a database user.
func (m *Mock) DeleteUser(_ context.Context, instance, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.users.Delete(childKey(instance, name)) {
		return cerrors.Newf(cerrors.NotFound, "user %q not found", name)
	}

	return nil
}

// ---- SSL certs ----

// CreateSslCert issues a client SSL certificate for an instance. The
// fingerprint is derived from the common name so it is stable across calls.
func (m *Mock) CreateSslCert(_ context.Context, cfg rdsdriver.SslCertConfig) (*rdsdriver.SslCert, error) {
	if cfg.CommonName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "commonName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireInstance(cfg.Instance); err != nil {
		return nil, err
	}

	sum := sha1.Sum([]byte(cfg.Instance + "/" + cfg.CommonName)) //nolint:gosec // fingerprint, not a security control.
	fingerprint := hex.EncodeToString(sum[:])

	cert := rdsdriver.SslCert{
		Instance:        cfg.Instance,
		CommonName:      cfg.CommonName,
		Sha1Fingerprint: fingerprint,
		Cert:            "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----",
		SerialNumber:    fingerprint[:16],
	}

	m.sslCerts.Set(childKey(cfg.Instance, fingerprint), cert)

	out := cert

	return &out, nil
}

// GetSslCert returns a single SSL cert by fingerprint.
func (m *Mock) GetSslCert(_ context.Context, instance, sha1FP string) (*rdsdriver.SslCert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cert, ok := m.sslCerts.Get(childKey(instance, sha1FP))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "sslCert %q not found", sha1FP)
	}

	out := cert

	return &out, nil
}

// ListSslCerts returns all SSL certs for an instance.
func (m *Mock) ListSslCerts(_ context.Context, instance string) ([]rdsdriver.SslCert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireInstance(instance); err != nil {
		return nil, err
	}

	out := []rdsdriver.SslCert{}

	for _, cert := range m.sslCerts.All() {
		if cert.Instance == instance {
			out = append(out, cert)
		}
	}

	return out, nil
}

// DeleteSslCert removes an SSL cert by fingerprint.
func (m *Mock) DeleteSslCert(_ context.Context, instance, sha1FP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.sslCerts.Delete(childKey(instance, sha1FP)) {
		return cerrors.Newf(cerrors.NotFound, "sslCert %q not found", sha1FP)
	}

	return nil
}

// ---- Instance actions ----

// FailoverInstance validates the instance exists and re-emits metrics. Cloud
// SQL failover promotes the standby of a regional instance; the mock keeps the
// instance available.
func (m *Mock) FailoverInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(id); !ok {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	m.emitInstanceMetrics(id, cpuMetricRunning, connRunning)

	return nil
}

// PromoteReplica validates the instance exists. Cloud SQL detaches the replica
// from its primary and makes it a standalone instance; the mock keeps it
// available.
func (m *Mock) PromoteReplica(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.instances.Get(id); !ok {
		return cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", id)
	}

	return nil
}

// CloneInstance copies sourceID to a new instance named destID.
func (m *Mock) CloneInstance(_ context.Context, sourceID, destID string) (*rdsdriver.Instance, error) {
	if destID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "destinationInstanceName is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.instances.Get(sourceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Cloud SQL instance %q not found", sourceID)
	}

	if _, ok := m.instances.Get(destID); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Cloud SQL instance %q already exists", destID)
	}

	clone := src
	clone.ID = destID
	clone.ARN = idgen.GCPID(m.opts.ProjectID, "instances", destID)
	clone.Endpoint = instanceConnectionName(m.opts.ProjectID, src.AvailabilityZone, destID)
	clone.State = rdsdriver.StateAvailable
	clone.CreatedAt = m.opts.Clock.Now().UTC()

	clone.VPCSecurityGroups = append([]string(nil), src.VPCSecurityGroups...)
	clone.Tags = copyTags(src.Tags)

	m.instances.Set(destID, clone)

	m.emitInstanceMetrics(destID, cpuMetricRunning, connRunning)

	out := clone

	return &out, nil
}
