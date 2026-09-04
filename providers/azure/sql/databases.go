package sql

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Azure SQL Database ARM status values (properties.status). A database is
// Creating on first provision and Scaling while a service-objective/SKU change
// applies, then settles to Online. See armsql.DatabaseStatus.
const (
	dbStatusCreating = "Creating"
	dbStatusScaling  = "Scaling"

	// createModeCopy / createModePITR are the Azure SQL createMode values that
	// provision a new database from an existing source database. Point-in-time
	// restore is modeled as a copy of the source's current state: in-memory
	// databases carry no row data, so there is no historical state to rewind to.
	createModeCopy = "Copy"
	createModePITR = "PointInTimeRestore"
)

// isSourceCopyMode reports whether a createMode provisions a database from an
// existing source database (properties.sourceDatabaseId).
func isSourceCopyMode(mode string) bool {
	return mode == createModeCopy || mode == createModePITR
}

// dbKey is the storage key for a logical database: "server/name".
func dbKey(server, name string) string { return server + "/" + name }

// DatabaseTransientStatus returns the ARM database status to report while a
// create/update settle window is active ("Creating" / "Scaling"), or "" once
// the database has settled (the wire layer then reports the terminal "Online").
// Always "" unless config.Options.AsyncSettle is set. It is the read-through the
// wire handler consults so a real armsql client observes the intermediate state.
func (m *Mock) DatabaseTransientStatus(server, name string) string {
	return m.dbSettle.State(dbKey(server, name), m.opts.Clock.Now(), "")
}

// CreateDatabase creates a logical database on a SQL server, implementing the
// relationaldb Databases optional capability. SKU/tier default to the common
// General Purpose Gen5 shape a discoverer can price when the request omits them.
//
//nolint:gocritic // cfg matches the Databases capability interface signature.
func (m *Mock) CreateDatabase(_ context.Context, cfg rdsdriver.DatabaseConfig) (*rdsdriver.Database, error) {
	if cfg.Server == "" || cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "server and database name are required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// A database is a child of a logical server: real Azure returns
	// ParentResourceNotFound (404) when the server has not been created.
	server, ok := m.clusters.Get(cfg.Server)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Azure SQL server %q not found", cfg.Server)
	}

	key := dbKey(cfg.Server, cfg.Name)
	if _, ok := m.databases.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "database %q already exists on server %q", cfg.Name, cfg.Server)
	}

	// A Copy / PointInTimeRestore create seeds the new database from an existing
	// source. Resolved before the elastic-pool check so a missing source is
	// reported as its own NotFound rather than a pool error.
	if isSourceCopyMode(cfg.CreateMode) {
		if err := m.applyCopySource(&cfg); err != nil {
			return nil, err
		}
	}

	if err := m.requireElasticPool(cfg.Server, cfg.ElasticPoolID); err != nil {
		return nil, err
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
		Location:      orDefault(cfg.Location, server.Location),
		Tags:          copyTags(cfg.Tags),
		SKUName:       skuName,
		SKUTier:       skuTier,
		SKUCapacity:   cfg.SKUCapacity,
		ZoneRedundant: cfg.ZoneRedundant,
		ElasticPoolID: cfg.ElasticPoolID,
	}
	m.databases.Set(key, db)

	// Under AsyncSettle the database reports status Creating until the window
	// elapses (real Azure SQL: Creating → Online). Default off → SettleDuration
	// 0 → inactive window → Online immediately.
	m.dbSettle.Begin(key, dbStatusCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultAzureDBSettle))

	// Azure SQL databases are encrypted at rest by default: a create
	// materializes the transparentDataEncryption/current sub-resource as
	// Enabled so a Get on it round-trips without a separate PUT.
	m.tde.Set(key, rdsdriver.TransparentDataEncryption{
		Server:   cfg.Server,
		Database: cfg.Name,
		State:    rdsdriver.TDEStateEnabled,
	})

	out := db

	return &out, nil
}

// applyCopySource resolves the copy/restore source database named by
// cfg.SourceDatabaseID and seeds cfg with the source's properties that the
// request left unset (collation, charset, SKU). The result is an independent,
// standalone database — the source's elastic-pool membership is not inherited.
// The caller holds m.mu, so the source read is on the already-locked store.
func (m *Mock) applyCopySource(cfg *rdsdriver.DatabaseConfig) error {
	if cfg.SourceDatabaseID == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "createMode %q requires sourceDatabaseId", cfg.CreateMode)
	}

	srcServer, srcName, ok := rdsdriver.SourceDatabaseRef(cfg.SourceDatabaseID)
	if !ok {
		// A bare name refers to a database on the same server.
		srcServer, srcName = cfg.Server, cfg.SourceDatabaseID
	}

	src, ok := m.databases.Get(dbKey(srcServer, srcName))
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "source database %q not found on server %q", srcName, srcServer)
	}

	if cfg.Collation == "" {
		cfg.Collation = src.Collation
	}

	if cfg.Charset == "" {
		cfg.Charset = src.Charset
	}

	if cfg.SKUName == "" {
		cfg.SKUName = src.SKUName
	}

	if cfg.SKUTier == "" {
		cfg.SKUTier = src.SKUTier
	}

	if cfg.SKUCapacity == 0 {
		cfg.SKUCapacity = src.SKUCapacity
	}

	return nil
}

// UpdateDatabase applies a fully-merged desired state to an existing logical
// database in place, implementing the relationaldb DatabaseUpdater optional
// capability. It is the update half of the wire CreateOrUpdate/PATCH: the
// caller has already merged the request body over the stored record, so cfg
// carries the resolved final values for every mutable field.
//
// Crucially it does NOT touch the transparentDataEncryption record: a database
// whose TDE was set to Disabled keeps that state across an unrelated property
// update (real Azure never re-enables TDE on a database PATCH). The mutation is
// a write-locked COW Update, so it is safe under concurrent access and never
// re-materializes TDE the way a delete+recreate upsert would.
//
//nolint:gocritic // cfg matches the DatabaseUpdater capability interface signature.
func (m *Mock) UpdateDatabase(_ context.Context, cfg rdsdriver.DatabaseConfig) (*rdsdriver.Database, error) {
	if cfg.Server == "" || cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "server and database name are required")
	}

	var updated rdsdriver.Database

	ok := m.databases.Update(dbKey(cfg.Server, cfg.Name), func(db rdsdriver.Database) rdsdriver.Database {
		db.Charset = cfg.Charset
		db.Collation = cfg.Collation
		db.Location = orDefault(cfg.Location, db.Location)
		db.Tags = copyTags(cfg.Tags)
		db.SKUName = cfg.SKUName
		db.SKUTier = cfg.SKUTier
		db.SKUCapacity = cfg.SKUCapacity
		db.ZoneRedundant = cfg.ZoneRedundant
		db.ElasticPoolID = cfg.ElasticPoolID
		updated = db

		return db
	})
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", cfg.Name, cfg.Server)
	}

	// Under AsyncSettle an updated database briefly reports status Scaling (a
	// service-objective/SKU change) before settling back to Online; a no-op when
	// settle is off.
	m.dbSettle.Begin(dbKey(cfg.Server, cfg.Name), dbStatusScaling, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultAzureDBSettle))

	out := updated
	out.Tags = copyTags(updated.Tags)

	return &out, nil
}

// GetDatabase returns a logical database, or NotFound.
func (m *Mock) GetDatabase(_ context.Context, server, name string) (*rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, ok := m.databases.Get(dbKey(server, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", name, server)
	}

	out := db
	out.Tags = copyTags(db.Tags)

	return &out, nil
}

// ListDatabases returns every logical database on a server.
func (m *Mock) ListDatabases(_ context.Context, server string) ([]rdsdriver.Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := []rdsdriver.Database{}

	vals := m.databases.SortedValues()
	for i := range vals {
		if vals[i].Server == server {
			db := vals[i]
			db.Tags = copyTags(db.Tags)
			out = append(out, db)
		}
	}

	return out, nil
}

// DeleteDatabase removes a logical database, or returns NotFound.
func (m *Mock) DeleteDatabase(_ context.Context, server, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.databases.Delete(dbKey(server, name)) {
		return cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", name, server)
	}

	m.tde.Delete(dbKey(server, name))
	m.dbSettle.Clear(dbKey(server, name))

	return nil
}
