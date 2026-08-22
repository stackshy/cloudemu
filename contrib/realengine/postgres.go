// Package realengine provides opt-in real database engines that back CloudEmu's
// relational-database instances, so clients can run actual SQL against the
// emulator instead of a synthetic endpoint.
//
// It is a separate module on purpose: its heavyweight dependency (a real
// Postgres binary, run without Docker via embedded-postgres) stays out of the
// core cloudemu module, preserving the in-memory, no-Docker default. Wire it in
// with config.WithDatabaseEngine(realengine.NewPostgres(0)).
package realengine

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/lib/pq"
	"github.com/stackshy/cloudemu/v2/config"
)

const (
	defaultPort     = 55432
	adminUser       = "postgres"
	adminPassword   = "postgres"
	adminDB         = "postgres"
	defaultRole     = "cloudemu"
	defaultPassword = "cloudemu"
)

// Postgres is a config.DatabaseEngine backed by one real embedded Postgres
// server. Each provisioned instance becomes its own database (and login role)
// inside that server. Safe for concurrent use within a process.
type Postgres struct {
	port int

	mu      sync.Mutex
	pg      *embeddedpostgres.EmbeddedPostgres
	started bool
	dbs     map[string]string // instanceID -> database name
}

// NewPostgres returns a Postgres engine that listens on port (0 = default). The
// server starts lazily on the first Provision.
func NewPostgres(port int) *Postgres {
	if port == 0 {
		port = defaultPort
	}

	return &Postgres{port: port, dbs: map[string]string{}}
}

// Provision creates a login role and database for the instance (making its
// master credentials usable) and returns the address to connect to.
//
//nolint:gocritic // req is the by-value DTO defined by the DatabaseEngine contract
func (p *Postgres) Provision(ctx context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.ensureStarted(); err != nil {
		return config.ProvisionResult{}, err
	}

	db, err := p.adminConn()
	if err != nil {
		return config.ProvisionResult{}, err
	}
	defer db.Close()

	role := orDefault(req.Username, defaultRole)
	password := orDefault(req.Password, defaultPassword)
	dbName := orDefault(req.DBName, req.InstanceID)

	if err := ensureRole(ctx, db, role, password); err != nil {
		return config.ProvisionResult{}, err
	}

	if err := ensureDatabase(ctx, db, dbName, role); err != nil {
		return config.ProvisionResult{}, err
	}

	p.dbs[req.InstanceID] = dbName

	return config.ProvisionResult{Host: "127.0.0.1", Port: p.port}, nil
}

// Deprovision drops the database backing the instance. No-op if unknown.
func (p *Postgres) Deprovision(ctx context.Context, instanceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	dbName, ok := p.dbs[instanceID]
	if !ok || !p.started {
		return nil
	}

	db, err := p.adminConn()
	if err != nil {
		return err
	}
	defer db.Close()

	// Terminate open connections so DROP DATABASE isn't blocked.
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, dbName); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(dbName)); err != nil {
		return err
	}

	delete(p.dbs, instanceID)

	return nil
}

// Close stops the embedded Postgres server. Safe to call more than once.
func (p *Postgres) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	p.started = false

	return p.pg.Stop()
}

func (p *Postgres) ensureStarted() error {
	if p.started {
		return nil
	}

	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(uint32(p.port)). //nolint:gosec // port is a small in-range int
			Username(adminUser).
			Password(adminPassword).
			Database(adminDB),
	)
	if err := pg.Start(); err != nil {
		return fmt.Errorf("start embedded postgres: %w", err)
	}

	p.pg = pg
	p.started = true

	return nil
}

func (p *Postgres) adminConn() (*sql.DB, error) {
	dsn := fmt.Sprintf("host=127.0.0.1 port=%d user=%s password=%s dbname=%s sslmode=disable",
		p.port, adminUser, adminPassword, adminDB)

	return sql.Open("postgres", dsn)
}

func ensureRole(ctx context.Context, db *sql.DB, role, password string) error {
	// CREATE ROLE cannot be parameterized; identifiers and the literal password
	// are quoted/escaped via lib/pq helpers, not user-formatted SQL.
	//nolint:gosec // G201: values are quoted via pq.QuoteLiteral/QuoteIdentifier
	stmt := fmt.Sprintf(
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %s) `+
			`THEN CREATE ROLE %s LOGIN SUPERUSER PASSWORD %s; END IF; END $$;`,
		pq.QuoteLiteral(role), pq.QuoteIdentifier(role), pq.QuoteLiteral(password),
	)

	_, err := db.ExecContext(ctx, stmt)

	return err
}

func ensureDatabase(ctx context.Context, db *sql.DB, name, owner string) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return err
	}

	if exists {
		return nil
	}

	_, err := db.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name)+" OWNER "+pq.QuoteIdentifier(owner))

	return err
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// staticEngineCheck asserts Postgres satisfies the config.DatabaseEngine
// contract at compile time.
var _ config.DatabaseEngine = (*Postgres)(nil)
