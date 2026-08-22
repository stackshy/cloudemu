// Package dbengine wires an optional real database engine into a relational
// provider's instance lifecycle. It is shared by every RDS-style provider
// (AWS RDS, Azure Flexible Server, GCP Cloud SQL) so the provision/deprovision
// hook stays identical across clouds and cannot drift.
package dbengine

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	enginePostgres       = "postgres"
	engineAuroraPostgres = "aurora-postgresql"
	engineMySQL          = "mysql"
	engineAuroraMySQL    = "aurora-mysql"
)

// IsPostgresFamily reports whether a real Postgres engine can back this engine
// family. The no-Docker embedded-postgres backing serves all Postgres-wire
// services (RDS/Aurora Postgres, Azure Flexible Server, Cloud SQL Postgres).
// Matching is case-insensitive and prefix-based on "postgres" — providers spell
// the engine differently ("postgres", "POSTGRES", "Postgres") and Cloud SQL
// uses a databaseVersion like "POSTGRES_15". Aurora Postgres ("aurora-postgresql")
// is matched exactly since it does not share the prefix. MySQL, SQL Server and
// an empty engine never match.
func IsPostgresFamily(engine string) bool {
	return strings.HasPrefix(strings.ToLower(engine), enginePostgres) || strings.EqualFold(engine, engineAuroraPostgres)
}

// IsMySQLFamily reports whether a real MySQL engine can back this engine family.
// Matching is case-insensitive and prefix-based on "mysql" — providers spell the
// engine differently ("mysql", "MYSQL_8_0", "MySQL"). Aurora MySQL
// ("aurora-mysql") is matched exactly since it does not share the prefix.
// MariaDB, SQL Server, Postgres and an empty engine never match.
func IsMySQLFamily(engine string) bool {
	return strings.HasPrefix(strings.ToLower(engine), engineMySQL) || strings.EqualFold(engine, engineAuroraMySQL)
}

// Provision backs the instance with the engine when one is configured and the
// engine family is supported, overriding the synthetic endpoint with the real
// host:port a client connects to. No-op otherwise.
//
// The single wired engine now receives BOTH the Postgres and the MySQL family;
// like the port caveat, this trusts the wired engine to serve every family it is
// handed — a mismatched single-family engine (e.g. a Postgres-only backing given
// a MySQL instance) fails loudly at connect time rather than here. Use
// NewMultiEngine to route each family to a dedicated backing.
func Provision(ctx context.Context, engine config.DatabaseEngine, inst *rdsdriver.Instance, cfg *rdsdriver.InstanceConfig) error {
	if engine == nil || (!IsPostgresFamily(cfg.Engine) && !IsMySQLFamily(cfg.Engine)) {
		return nil
	}

	res, err := engine.Provision(ctx, config.ProvisionRequest{
		InstanceID: cfg.ID,
		Engine:     cfg.Engine,
		DBName:     cfg.DBName,
		Username:   cfg.MasterUsername,
		Password:   cfg.MasterUserPassword,
	})
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "provision database engine: %v", err)
	}

	inst.Endpoint = res.Host
	inst.Port = res.Port

	return nil
}

// RotatePassword re-runs the engine role/user upsert with a new master password
// so the rotated credential authenticates, reusing the instance's existing
// engine key, username, engine family and database name. It is an idempotent
// Provision against the already-provisioned instance (the Postgres engine ALTERs
// the role, the MySQL engine recreates the user), so the returned host:port are
// unchanged. No-op when no engine backs this family or newPassword is empty.
func RotatePassword(ctx context.Context, engine config.DatabaseEngine, inst *rdsdriver.Instance, newPassword string) error {
	if newPassword == "" {
		return nil
	}

	cfg := rdsdriver.InstanceConfig{
		ID:                 inst.ID,
		Engine:             inst.Engine,
		DBName:             inst.DBName,
		MasterUsername:     inst.MasterUsername,
		MasterUserPassword: newPassword,
	}

	return Provision(ctx, engine, inst, &cfg)
}

// Deprovision tears down the real database backing the instance, if any.
func Deprovision(ctx context.Context, engine config.DatabaseEngine, inst *rdsdriver.Instance) error {
	if engine == nil || (!IsPostgresFamily(inst.Engine) && !IsMySQLFamily(inst.Engine)) {
		return nil
	}

	if err := engine.Deprovision(ctx, inst.ID); err != nil {
		return cerrors.Newf(cerrors.Internal, "deprovision database engine: %v", err)
	}

	return nil
}

// FamilyEngine pairs a family predicate with the engine that serves it, for use
// with NewMultiEngine.
type FamilyEngine struct {
	Match  func(engine string) bool
	Engine config.DatabaseEngine
}

// multiEngine is a config.DatabaseEngine that dispatches each instance to the
// first entry whose Match accepts the request engine, and remembers the choice
// so Deprovision — which carries no engine string — routes back to the same
// backing. It is a core type and must not import contrib.
type multiEngine struct {
	entries []FamilyEngine
	mu      sync.Mutex
	byID    map[string]config.DatabaseEngine // instanceID -> chosen engine
}

// NewMultiEngine returns a composite config.DatabaseEngine so one
// Options.DatabaseEngine can serve multiple families (e.g. Postgres and MySQL
// backed by different engines). Provision tries each entry's Match against the
// request engine in order and delegates to the first match; an unmatched engine
// is a no-op (nil result, nil error), matching the current single-engine
// behavior. Deprovision routes to whichever engine provisioned that instance ID.
func NewMultiEngine(entries ...FamilyEngine) config.DatabaseEngine {
	return &multiEngine{entries: entries, byID: map[string]config.DatabaseEngine{}}
}

// Provision delegates to the first entry whose Match accepts req.Engine and
// remembers the instanceID→engine mapping for Deprovision. Unmatched → no-op.
//
//nolint:gocritic // req is passed by value to satisfy the config.DatabaseEngine interface.
func (m *multiEngine) Provision(ctx context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	for _, e := range m.entries {
		if e.Match == nil || e.Engine == nil || !e.Match(req.Engine) {
			continue
		}

		res, err := e.Engine.Provision(ctx, req)
		if err != nil {
			return config.ProvisionResult{}, err
		}

		m.mu.Lock()
		m.byID[req.InstanceID] = e.Engine
		m.mu.Unlock()

		return res, nil
	}

	return config.ProvisionResult{}, nil
}

// Deprovision routes to the engine that provisioned the instance, if any, and
// forgets the mapping. Unknown IDs are a no-op.
func (m *multiEngine) Deprovision(ctx context.Context, instanceID string) error {
	m.mu.Lock()
	engine, ok := m.byID[instanceID]
	delete(m.byID, instanceID)
	m.mu.Unlock()

	if !ok {
		return nil
	}

	return engine.Deprovision(ctx, instanceID)
}
