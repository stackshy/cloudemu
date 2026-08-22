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
	"errors"
	"fmt"
	"strings"
	"sync"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/lib/pq"
	"github.com/stackshy/cloudemu/v2/config"
)

const (
	// defaultPort is the standard PostgreSQL port. Azure PostgreSQL Flexible
	// Server and GCP Cloud SQL never surface a port in their SDK responses —
	// clients always connect on 5432 — so the engine must listen there for a
	// real client to connect using only the SDK response. (AWS RDS surfaces the
	// port explicitly and works on any port.) Only one Postgres server can bind
	// 5432 on a host; pass an explicit port to co-host more than one.
	defaultPort = 5432
	// adminUser is the engine's internal bootstrap superuser. It is deliberately
	// NOT "postgres": Cloud SQL's fixed root user is "postgres", so a provisioned
	// tenant role by that name must be free to be created (and given the caller's
	// rootPassword) without colliding with — or overwriting the password of — the
	// maintenance superuser the engine itself connects as. adminDB stays the
	// default "postgres" maintenance database, which initdb always creates.
	adminUser       = "cloudemu_superuser"
	adminPassword   = "cloudemu_superuser"
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

// errReservedRole is returned when a tenant master username collides with the
// engine's internal maintenance superuser.
var errReservedRole = errors.New("realengine: master username is reserved")

func ensureRole(ctx context.Context, db *sql.DB, role, password string) error {
	// Reject the engine's own maintenance superuser: silently honoring it would
	// report success while the caller's password never authenticates (the role
	// keeps the internal admin password). Fail loudly, matching how the MySQL
	// engine rejects "root".
	if strings.EqualFold(role, adminUser) {
		return fmt.Errorf("%q: %w", role, errReservedRole)
	}

	// Upsert the password on every provision, not just create: providers that
	// pin a fixed master username (e.g. Cloud SQL's "postgres") reuse one role
	// across instances on this shared server, so the role must adopt the
	// most-recently-provisioned instance's password — otherwise a second or a
	// re-created instance's credentials, which the API told the caller to use,
	// would silently fail to authenticate. (Concurrent instances that pin the
	// same username therefore share one password: last writer wins. Distinct
	// usernames — the common RDS/Azure case — are fully independent.)
	//
	// CREATE/ALTER ROLE cannot be parameterized; identifiers and the literal
	// password are quoted/escaped via lib/pq helpers, not user-formatted SQL.
	//nolint:gosec // G201: values are quoted via pq.QuoteLiteral/QuoteIdentifier
	stmt := fmt.Sprintf(
		`DO $$ BEGIN `+
			`IF EXISTS (SELECT FROM pg_roles WHERE rolname = %[1]s) `+
			`THEN ALTER ROLE %[2]s LOGIN SUPERUSER PASSWORD %[3]s; `+
			`ELSE CREATE ROLE %[2]s LOGIN SUPERUSER PASSWORD %[3]s; END IF; END $$;`,
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
