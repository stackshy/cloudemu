package athena

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// Catalog is the part of the AWS Glue Data Catalog that Athena reads and
// writes. Athena's AwsDataCatalog is the Glue Data Catalog, so databases and
// tables live in Glue and both services see the same ones. See
// https://docs.aws.amazon.com/athena/latest/ug/data-sources-glue.html.
//
// The Glue mock satisfies it. Implementations must not call back into Athena,
// because the legacy snapshot import holds a lock across catalog calls.
type Catalog interface {
	CreateDatabase(ctx context.Context, catalogID string, db gluedriver.Database) error
	GetDatabase(ctx context.Context, catalogID, name string) (*gluedriver.Database, error)
	DeleteDatabase(ctx context.Context, catalogID, name string) error
	GetDatabases(ctx context.Context, catalogID string, page gluedriver.TablePagination) ([]gluedriver.Database, string, error)
	GetTable(ctx context.Context, catalogID, dbName, name string) (*gluedriver.Table, error)
	GetTables(ctx context.Context, catalogID, dbName string, page gluedriver.TablePagination) ([]gluedriver.Table, string, error)
}

// connectionLookup is the optional part of Catalog that finds Glue
// connections. A FEDERATED catalog built on a connection-arn needs it. The
// fallback store has no connections, so it skips the check.
type connectionLookup interface {
	GetConnection(ctx context.Context, catalogID, name string) (*gluedriver.Connection, error)
}

// connectorNotSupported is the failure for catalogs served by a connector.
const connectorNotSupported = "NOT_SUPPORTED: federated/Lambda connector execution is not emulated"

// catalogIDParam is the GLUE data catalog parameter naming the Glue catalog.
const catalogIDParam = "catalog-id"

// catalogPageSize is the page size used to drain Glue list calls.
const catalogPageSize = 100

// SetCatalog wires the Glue Data Catalog that backs AwsDataCatalog and every
// GLUE type catalog. A nil catalog keeps the built-in fallback store, which
// is what a standalone athena.New uses.
func (m *Mock) SetCatalog(c Catalog) {
	if c == nil {
		return
	}

	m.catalog = c
}

// glueCatalogID maps an Athena data catalog name to the Glue catalog id it
// reads. AwsDataCatalog is the account's default Glue catalog. A GLUE catalog
// may name a catalog-id, which must be this account. Other catalog types have
// no emulated connector.
func (m *Mock) glueCatalogID(name string) (string, error) {
	if name == "" || strings.EqualFold(name, driver.DefaultDataCatalog) {
		return "", nil
	}

	dc, ok := m.dataCatalogs.Get(name)
	if !ok {
		return "", catalogNotFound(name)
	}

	if !strings.EqualFold(dc.Type, driver.DataCatalogTypeGlue) {
		return "", invalidRequest("%s", connectorNotSupported)
	}

	id := dc.Parameters[catalogIDParam]
	if id != "" && id != m.opts.AccountID {
		return "", invalidRequest("NOT_SUPPORTED: cross-account Glue catalog %s is not emulated", id)
	}

	return id, nil
}

// allDatabases drains every Glue database of a catalog.
func allDatabases(ctx context.Context, c Catalog, catalogID string) ([]gluedriver.Database, error) {
	var out []gluedriver.Database

	page := gluedriver.TablePagination{MaxResults: catalogPageSize}

	for {
		dbs, next, err := c.GetDatabases(ctx, catalogID, page)
		if err != nil {
			return nil, err
		}

		out = append(out, dbs...)

		if next == "" {
			return out, nil
		}

		page.NextToken = next
	}
}

// allTables drains every Glue table of a database.
func allTables(ctx context.Context, c Catalog, catalogID, dbName string) ([]gluedriver.Table, error) {
	var out []gluedriver.Table

	page := gluedriver.TablePagination{MaxResults: catalogPageSize}

	for {
		tables, next, err := c.GetTables(ctx, catalogID, dbName, page)
		if err != nil {
			return nil, err
		}

		out = append(out, tables...)

		if next == "" {
			return out, nil
		}

		page.NextToken = next
	}
}

// localCatalog is the Glue shaped store a standalone Athena mock uses when no
// Glue catalog is wired. It only holds databases, since Athena DDL creates no
// tables yet.
type localCatalog struct {
	accountID string
	dbs       *memstore.Store[gluedriver.Database]
}

func newLocalCatalog(accountID string) *localCatalog {
	return &localCatalog{accountID: accountID, dbs: memstore.New[gluedriver.Database]()}
}

func (l *localCatalog) key(catalogID, name string) string {
	if catalogID == "" {
		catalogID = l.accountID
	}

	return catalogID + keySep + name
}

//nolint:gocritic // hugeParam: matches the Glue catalog signature
func (l *localCatalog) CreateDatabase(_ context.Context, catalogID string, db gluedriver.Database) error {
	if db.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "database name is required")
	}

	db.Parameters = copyStringMap(db.Parameters)

	if !l.dbs.SetIfAbsent(l.key(catalogID, db.Name), db) {
		return cerrors.Newf(cerrors.AlreadyExists, "Database already exists: %s", db.Name)
	}

	return nil
}

func (l *localCatalog) GetDatabase(_ context.Context, catalogID, name string) (*gluedriver.Database, error) {
	db, ok := l.dbs.Get(l.key(catalogID, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Database not found: %s", name)
	}

	db.Parameters = copyStringMap(db.Parameters)

	return &db, nil
}

func (l *localCatalog) DeleteDatabase(_ context.Context, catalogID, name string) error {
	if !l.dbs.Delete(l.key(catalogID, name)) {
		return cerrors.Newf(cerrors.NotFound, "Database not found: %s", name)
	}

	return nil
}

func (l *localCatalog) GetDatabases(
	_ context.Context, catalogID string, _ gluedriver.TablePagination,
) ([]gluedriver.Database, string, error) {
	prefix := l.key(catalogID, "")
	out := make([]gluedriver.Database, 0)

	for _, k := range sortedKeys(l.dbs.Keys()) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		if db, ok := l.dbs.Get(k); ok {
			db.Parameters = copyStringMap(db.Parameters)
			out = append(out, db)
		}
	}

	return out, "", nil
}

func (l *localCatalog) GetTable(_ context.Context, catalogID, dbName, name string) (*gluedriver.Table, error) {
	if _, ok := l.dbs.Get(l.key(catalogID, dbName)); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Database not found: %s", dbName)
	}

	return nil, cerrors.Newf(cerrors.NotFound, "Table not found: %s", name)
}

func (l *localCatalog) GetTables(
	_ context.Context, catalogID, dbName string, _ gluedriver.TablePagination,
) ([]gluedriver.Table, string, error) {
	if _, ok := l.dbs.Get(l.key(catalogID, dbName)); !ok {
		return nil, "", cerrors.Newf(cerrors.NotFound, "Database not found: %s", dbName)
	}

	return []gluedriver.Table{}, "", nil
}
