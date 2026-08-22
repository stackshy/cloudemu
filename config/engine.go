package config

import "context"

// DatabaseEngine optionally backs relational-database instances with a real
// engine (e.g. a real Postgres) so that clients can run actual SQL against the
// emulator. When nil — the default — instances use synthetic endpoints and no
// real database runs, keeping the emulator in-memory and dependency-free.
//
// Implementations live outside the core module (contrib/) so the core carries
// no database-engine dependency; this is a pluggable capability like Clock.
type DatabaseEngine interface {
	// Provision starts or creates a real database for the instance and returns
	// the host and port a client connects to. It must make the instance's
	// master credentials usable so a caller can connect with them.
	Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error)

	// Deprovision tears down the real database backing the instance. It is a
	// no-op if the instance was never provisioned.
	Deprovision(ctx context.Context, instanceID string) error
}

// ProvisionRequest describes the database a DatabaseEngine should back.
type ProvisionRequest struct {
	InstanceID string
	Engine     string // "postgres", "mysql", …
	DBName     string // optional initial database name
	Username   string // master username the caller will connect with
	Password   string // master password the caller will connect with
}

// ProvisionResult is the reachable address of the provisioned database.
type ProvisionResult struct {
	Host string
	Port int
}

// CacheEngine optionally backs cache instances with a real cache server (e.g. a
// real Redis) so clients can run real commands against the emulator. When nil —
// the default — caches keep a synthetic endpoint and no real server runs.
//
// Like DatabaseEngine, implementations live outside the core module (contrib/).
type CacheEngine interface {
	// Provision starts a real cache server for the instance and returns the
	// host and port a client connects to.
	Provision(ctx context.Context, req CacheProvisionRequest) (ProvisionResult, error)

	// Deprovision tears down the real cache server backing the instance. It is a
	// no-op if the instance was never provisioned.
	Deprovision(ctx context.Context, cacheID string) error
}

// CacheProvisionRequest describes the cache a CacheEngine should back.
type CacheProvisionRequest struct {
	CacheID string
	Engine  string // "redis", "memcached"
}
