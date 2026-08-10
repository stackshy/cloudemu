package glue

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// databaseData is a database plus its own lock.
type databaseData struct {
	db driver.Database
	mu sync.RWMutex
}

// CreateDatabase creates a Data Catalog database, claiming the name atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateDatabase(_ context.Context, catalogID string, db driver.Database) error {
	cat := m.catalogOrDefault(catalogID)

	if !validName(db.Name) {
		return invalidInput("database name %q is invalid", db.Name)
	}

	db.CatalogID = cat
	db.CreateTime = m.now()
	stored := copyDatabase(db)

	if !m.databases.SetIfAbsent(nameKey(cat, db.Name), &databaseData{db: stored}) {
		return alreadyExists("Database already exists: %s", db.Name)
	}

	return nil
}

func (m *Mock) getDatabaseData(catalogID, name string) (*databaseData, string, error) {
	cat := m.catalogOrDefault(catalogID)

	if !validName(name) {
		return nil, cat, invalidInput("database name %q is invalid", name)
	}

	dd, ok := m.databases.Get(nameKey(cat, name))
	if !ok {
		return nil, cat, entityNotFound("Database not found: %s", name)
	}

	return dd, cat, nil
}

// GetDatabase returns a deep copy of a database.
func (m *Mock) GetDatabase(_ context.Context, catalogID, name string) (*driver.Database, error) {
	dd, _, err := m.getDatabaseData(catalogID, name)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	out := copyDatabase(dd.db)

	return &out, nil
}

// UpdateDatabase replaces a database's mutable fields.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateDatabase(_ context.Context, catalogID, name string, db driver.Database) error {
	dd, cat, err := m.getDatabaseData(catalogID, name)
	if err != nil {
		return err
	}

	if !validName(db.Name) {
		return invalidInput("database name %q is invalid", db.Name)
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	// A rename must not collide with an existing database.
	if db.Name != name {
		renamed := copyDatabase(db)
		renamed.CatalogID = cat
		renamed.CreateTime = dd.db.CreateTime

		if !m.databases.SetIfAbsent(nameKey(cat, db.Name), &databaseData{db: renamed}) {
			return alreadyExists("Database already exists: %s", db.Name)
		}

		m.databases.Delete(nameKey(cat, name))

		return nil
	}

	created := dd.db.CreateTime
	dd.db = copyDatabase(db)
	dd.db.CatalogID = cat
	dd.db.Name = name
	dd.db.CreateTime = created

	return nil
}

// DeleteDatabase removes a database and every table/UDF it contains, matching
// real Glue's cascade so dependents are not orphaned.
func (m *Mock) DeleteDatabase(_ context.Context, catalogID, name string) error {
	_, cat, err := m.getDatabaseData(catalogID, name)
	if err != nil {
		return err
	}

	// Hold the database scope lock across the whole cascade so a concurrent
	// CreateTable/CreateUserDefinedFunction can't orphan a child after we've
	// swept, and so a reader can't observe a torn snapshot.
	lock := m.scopeLock(nameKey(cat, name))
	lock.Lock()
	defer lock.Unlock()

	m.databases.Delete(nameKey(cat, name))

	tablePrefix := nameKey(cat, name) + keySep

	// Take each table's scope lock while sweeping its partitions so a concurrent
	// CreatePartition (which holds that lock) can't insert an orphan.
	for _, key := range m.tables.Keys() {
		if !strings.HasPrefix(key, tablePrefix) {
			continue
		}

		tblLock := m.scopeLock(key)
		tblLock.Lock()
		m.tables.Delete(key)

		partPrefix := key + keySep
		for _, pkey := range m.partitions.Keys() {
			if strings.HasPrefix(pkey, partPrefix) {
				m.partitions.Delete(pkey)
			}
		}
		tblLock.Unlock()
	}

	for _, key := range m.udfs.Keys() {
		if strings.HasPrefix(key, tablePrefix) {
			m.udfs.Delete(key)
		}
	}

	return nil
}

// GetDatabases lists databases in a catalog with pagination.
func (m *Mock) GetDatabases(
	_ context.Context, catalogID string, page driver.TablePagination,
) ([]driver.Database, string, error) {
	cat := m.catalogOrDefault(catalogID)
	prefix := cat + keySep

	keys := sortedKeys(m.databases.Keys())
	all := make([]driver.Database, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		dd, ok := m.databases.Get(key)
		if !ok {
			continue
		}

		dd.mu.RLock()
		all = append(all, copyDatabase(dd.db))
		dd.mu.RUnlock()
	}

	pageItems, next, err := paginate(all, page)
	if err != nil {
		return nil, "", err
	}

	return pageItems, next, nil
}
