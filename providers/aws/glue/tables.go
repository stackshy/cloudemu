package glue

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// tableData is a table, its version history, and its own lock.
type tableData struct {
	table    driver.Table
	versions []driver.TableVersion
	nextVer  int64
	mu       sync.RWMutex
}

// requireDatabase errors with EntityNotFound if the parent database is absent,
// so a table create can't orphan itself under a non-existent database.
func (m *Mock) requireDatabase(cat, db string) error {
	if !m.databases.Has(nameKey(cat, db)) {
		return entityNotFound("Database not found: %s", db)
	}

	return nil
}

// CreateTable creates a table under a database, claiming its name atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateTable(_ context.Context, catalogID, dbName string, tbl driver.Table) error {
	cat := m.catalogOrDefault(catalogID)

	if !validName(dbName) || !validName(tbl.Name) {
		return invalidInput("table or database name is invalid")
	}

	if err := m.requireDatabase(cat, dbName); err != nil {
		return err
	}

	now := m.now()
	tbl.CatalogID = cat
	tbl.DatabaseName = dbName
	tbl.CreateTime = now
	tbl.UpdateTime = now
	tbl.VersionID = "1"
	stored := copyTable(tbl)

	td := &tableData{
		table:    stored,
		versions: []driver.TableVersion{{Table: copyTable(tbl), VersionID: "1"}},
		nextVer:  2,
	}

	if !m.tables.SetIfAbsent(nameKey(cat, dbName, tbl.Name), td) {
		return alreadyExists("Table already exists: %s", tbl.Name)
	}

	return nil
}

func (m *Mock) getTableData(catalogID, dbName, name string) (*tableData, string, error) {
	cat := m.catalogOrDefault(catalogID)

	if !validName(dbName) || !validName(name) {
		return nil, cat, invalidInput("table or database name is invalid")
	}

	td, ok := m.tables.Get(nameKey(cat, dbName, name))
	if !ok {
		return nil, cat, entityNotFound("Table not found: %s", name)
	}

	return td, cat, nil
}

// GetTable returns a deep copy of a table.
func (m *Mock) GetTable(_ context.Context, catalogID, dbName, name string) (*driver.Table, error) {
	td, _, err := m.getTableData(catalogID, dbName, name)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	out := copyTable(td.table)

	return &out, nil
}

// UpdateTable replaces a table's mutable fields and appends a version.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateTable(_ context.Context, catalogID, dbName string, tbl driver.Table) error {
	td, cat, err := m.getTableData(catalogID, dbName, tbl.Name)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	created := td.table.CreateTime
	verID := strconv.FormatInt(td.nextVer, 10)

	updated := copyTable(tbl)
	updated.CatalogID = cat
	updated.DatabaseName = dbName
	updated.CreateTime = created
	updated.UpdateTime = m.now()
	updated.VersionID = verID

	td.table = updated
	td.versions = append(td.versions, driver.TableVersion{Table: copyTable(updated), VersionID: verID})
	td.nextVer++

	return nil
}

// DeleteTable removes a table, its versions, and its partitions under the write
// lock so a concurrent read can't observe a half-deleted table.
func (m *Mock) DeleteTable(_ context.Context, catalogID, dbName, name string) error {
	td, cat, err := m.getTableData(catalogID, dbName, name)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	m.tables.Delete(nameKey(cat, dbName, name))

	partPrefix := nameKey(cat, dbName, name) + keySep
	for _, key := range m.partitions.Keys() {
		if strings.HasPrefix(key, partPrefix) {
			m.partitions.Delete(key)
		}
	}

	return nil
}

// GetTables lists tables under a database with pagination.
func (m *Mock) GetTables(
	_ context.Context, catalogID, dbName string, page driver.TablePagination,
) ([]driver.Table, string, error) {
	cat := m.catalogOrDefault(catalogID)

	if err := m.requireDatabase(cat, dbName); err != nil {
		return nil, "", err
	}

	prefix := nameKey(cat, dbName) + keySep
	keys := sortedKeys(m.tables.Keys())
	all := make([]driver.Table, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		td, ok := m.tables.Get(key)
		if !ok {
			continue
		}

		td.mu.RLock()
		all = append(all, copyTable(td.table))
		td.mu.RUnlock()
	}

	return paginate(all, page)
}

// SearchTables returns tables across all databases in a catalog. The emulator
// does not evaluate the property-predicate search grammar; it returns every
// table (paginated), which is a superset of any predicate's matches.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) SearchTables(
	_ context.Context, catalogID string, page driver.TablePagination,
) ([]driver.Table, string, error) {
	cat := m.catalogOrDefault(catalogID)
	prefix := cat + keySep
	keys := sortedKeys(m.tables.Keys())
	all := make([]driver.Table, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		td, ok := m.tables.Get(key)
		if !ok {
			continue
		}

		td.mu.RLock()
		all = append(all, copyTable(td.table))
		td.mu.RUnlock()
	}

	return paginate(all, page)
}

// BatchDeleteTable deletes several tables, collecting per-table errors instead
// of failing the whole call. Names are validated before any delete.
func (m *Mock) BatchDeleteTable(
	_ context.Context, catalogID, dbName string, names []string,
) ([]driver.BatchError, error) {
	cat := m.catalogOrDefault(catalogID)

	if err := m.requireDatabase(cat, dbName); err != nil {
		return nil, err
	}

	for _, n := range names {
		if !validName(n) {
			return nil, invalidInput("table name %q is invalid", n)
		}
	}

	var errs []driver.BatchError

	for _, n := range names {
		if err := m.DeleteTable(context.Background(), cat, dbName, n); err != nil {
			errs = append(errs, driver.BatchError{
				Name: n, ErrorCode: driver.ExEntityNotFound, ErrorMessage: err.Error(),
			})
		}
	}

	return errs, nil
}
