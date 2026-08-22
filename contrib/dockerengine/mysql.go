package dockerengine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" database/sql driver
	"github.com/stackshy/cloudemu/v2/config"
)

const (
	// defaultMySQLPort is the standard MySQL port. Azure MySQL Flexible Server and
	// GCP Cloud SQL never surface a port in their SDK responses — clients always
	// connect on 3306 — so the engine must publish it there for a real client to
	// connect using only the SDK response. (AWS RDS surfaces the port explicitly.)
	// Only one container can bind 3306 on a host; pass an explicit port to co-host
	// more than one MySQL engine.
	defaultMySQLPort = 3306
	// mysqlImage is the container image the engine runs. Pin the major.minor so the
	// behavior does not drift under the caller.
	mysqlImage = "mysql:8.0"
	// rootUser / rootPassword are the container's internal bootstrap superuser.
	// A provisioned tenant may not reuse the root name — Provision rejects it
	// (real RDS/Azure reserve it too) rather than silently reporting success with
	// credentials that can't authenticate. rootPassword is local state for a
	// throwaway container, never a secret store, and is never logged.
	rootUser = "root"
	// rootPassword is a throwaway container password, never a real secret.
	//nolint:gosec // G101: not a credential — the local container's disposable root password
	rootPassword = "cloudemu-root"

	defaultDBName   = "cloudemu"
	defaultUser     = "cloudemu"
	defaultUserPass = "cloudemu"
	anyHost         = "'%'" // grant/user host: reachable over TCP from any docker network address

	// readyTimeout bounds how long ensureStarted waits for the image to finish its
	// first-boot initialization (it refuses connections until then). pollInterval
	// paces the readiness probe; pingTimeout bounds a single probe.
	readyTimeout = 120 * time.Second
	pollInterval = 500 * time.Millisecond
	pingTimeout  = 3 * time.Second
)

// Sentinel errors (err113): wrapped at the return site so callers can match and
// the linter stays satisfied without dynamic error creation.
var (
	errMySQLNotReady = errors.New("dockerengine: mysql container did not become ready in time")
	errBadIdent      = errors.New("dockerengine: invalid SQL identifier")
	errRootReserved  = errors.New("dockerengine: master username \"root\" is reserved")
)

// tenant records the database and login user provisioned for one instance so
// Deprovision — which carries no engine detail — can drop exactly what was made.
type tenant struct {
	dbName string
	user   string
}

// MySQL is a config.DatabaseEngine backed by one real MySQL server running in a
// Docker container. Each provisioned instance becomes its own database (and login
// user) inside that server. The container starts lazily on the first Provision.
// Safe for concurrent use within a process.
type MySQL struct {
	port int

	mu          sync.Mutex
	runner      Runner
	containerID string
	started     bool
	tenants     map[string]tenant // instanceID -> provisioned db + user
}

// NewMySQL returns a MySQL engine that publishes the container's port on the host
// (0 = default 3306). The container starts lazily on the first Provision.
func NewMySQL(port int) *MySQL {
	if port == 0 {
		port = defaultMySQLPort
	}

	return &MySQL{port: port, tenants: map[string]tenant{}}
}

// Provision creates a database and login user for the instance (making its master
// credentials usable) and returns the address to connect to.
//
//nolint:gocritic // req is the by-value DTO defined by the DatabaseEngine contract
func (m *MySQL) Provision(ctx context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureStarted(ctx); err != nil {
		return config.ProvisionResult{}, err
	}

	db, err := m.adminConn()
	if err != nil {
		return config.ProvisionResult{}, err
	}
	defer db.Close()

	user := orDefault(req.Username, defaultUser)
	password := orDefault(req.Password, defaultUserPass)
	dbName := orDefault(orDefault(req.DBName, req.InstanceID), defaultDBName)

	if err := ensureDatabase(ctx, db, dbName); err != nil {
		return config.ProvisionResult{}, err
	}

	if err := ensureUser(ctx, db, user, password, dbName); err != nil {
		return config.ProvisionResult{}, err
	}

	m.tenants[req.InstanceID] = tenant{dbName: dbName, user: user}

	return config.ProvisionResult{Host: "127.0.0.1", Port: m.port}, nil
}

// Deprovision drops the database and login user backing the instance (never the
// root superuser). No-op if the instance is unknown.
func (m *MySQL) Deprovision(ctx context.Context, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[instanceID]
	if !ok || !m.started {
		return nil
	}

	db, err := m.adminConn()
	if err != nil {
		return err
	}
	defer db.Close()

	ident, err := quoteIdent(t.dbName)
	if err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+ident); err != nil {
		return err
	}

	// Never drop the container's root superuser.
	if !strings.EqualFold(t.user, rootUser) {
		if _, err := db.ExecContext(ctx, "DROP USER IF EXISTS "+quoteUser(t.user)); err != nil {
			return err
		}
	}

	delete(m.tenants, instanceID)

	return nil
}

// Close force-removes the container. Safe to call more than once.
func (m *MySQL) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	m.started = false

	return m.runner.Rm(context.Background(), m.containerID)
}

// ensureStarted runs the container (once) and blocks until it accepts
// connections. The caller holds the lock.
func (m *MySQL) ensureStarted(ctx context.Context) error {
	if m.started {
		return nil
	}

	id, err := m.runContainer(ctx)
	if err != nil {
		return err
	}

	m.containerID = id

	if err := m.waitReady(ctx); err != nil {
		_ = m.runner.Rm(context.Background(), id)

		return err
	}

	m.started = true

	return nil
}

// runContainer starts a detached MySQL container with the host port published.
// The Runner does not publish ports, so the argv is built here; every argument is
// first-party (never a shell string).
func (m *MySQL) runContainer(ctx context.Context) (string, error) {
	portMap := fmt.Sprintf("%d:%d", m.port, defaultMySQLPort)
	env := "MYSQL_ROOT_PASSWORD=" + rootPassword

	out, err := exec.CommandContext(ctx, dockerBinary, "run", "-d", "-p", portMap, "-e", env, mysqlImage).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", errRun, strings.TrimSpace(string(out)), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// waitReady polls a root Ping with backoff until the server is up or the timeout
// elapses (the image refuses connections until first-boot init completes).
func (m *MySQL) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(readyTimeout)

	for time.Now().Before(deadline) {
		if m.pingOnce(ctx) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return errMySQLNotReady
}

// pingOnce reports whether a fresh root connection can be established right now.
func (m *MySQL) pingOnce(ctx context.Context) bool {
	db, err := m.adminConn()
	if err != nil {
		return false
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	return db.PingContext(pingCtx) == nil
}

func (m *MySQL) adminConn() (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(127.0.0.1:%d)/", rootUser, rootPassword, m.port)

	return sql.Open("mysql", dsn)
}

func ensureDatabase(ctx context.Context, db *sql.DB, name string) error {
	ident, err := quoteIdent(name)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+ident)

	return err
}

// ensureUser creates the login user (or, on reuse, resets its password) and grants
// it the database. CREATE then ALTER upserts the password — the same
// last-writer-wins rationale as the Postgres ensureRole: a re-provisioned or
// same-username instance's credentials (which the API told the caller to use) must
// authenticate, so the user adopts the most-recently-provisioned password.
func ensureUser(ctx context.Context, db *sql.DB, user, password, dbName string) error {
	// Reject the container's root superuser: honoring it would report success
	// while the caller's password never authenticates (root keeps its internal
	// password), so fail loudly the way real RDS/Azure reject a "root" master user.
	if strings.EqualFold(user, rootUser) {
		return fmt.Errorf("%q: %w", user, errRootReserved)
	}

	quotedUser := quoteUser(user)
	quotedPass := quoteLiteral(password)

	// Drop and recreate for a clean slate. MySQL accounts are server-global, so
	// two instances that pin the same master username share one account; a plain
	// CREATE-IF-NOT-EXISTS + GRANT would let its privileges ACCUMULATE across every
	// instance's database (cross-tenant access) and keep a stale password.
	// Recreating scopes the account to exactly this instance's database with this
	// instance's password — last writer wins for a shared username, distinct
	// usernames stay independent (documented in the README).
	if _, err := db.ExecContext(ctx, "DROP USER IF EXISTS "+quotedUser); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, "CREATE USER "+quotedUser+" IDENTIFIED BY "+quotedPass); err != nil {
		return err
	}

	ident, err := quoteIdent(dbName)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON "+ident+".* TO "+quotedUser)

	return err
}

// quoteUser renders a 'user'@'%' account name from a bare username. Identifiers
// and literals cannot be parameterized in DDL, so they are escaped here.
func quoteUser(user string) string {
	return quoteLiteral(user) + "@" + anyHost
}

// quoteIdent wraps a schema identifier in backticks, doubling any embedded
// backtick. Empty identifiers are rejected.
func quoteIdent(s string) (string, error) {
	if s == "" {
		return "", errBadIdent
	}

	return "`" + strings.ReplaceAll(s, "`", "``") + "`", nil
}

// quoteLiteral renders a single-quoted SQL string literal, escaping backslashes
// and single quotes.
func quoteLiteral(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "''")

	return "'" + s + "'"
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// staticEngineCheck asserts MySQL satisfies the config.DatabaseEngine contract at
// compile time.
var _ config.DatabaseEngine = (*MySQL)(nil)
