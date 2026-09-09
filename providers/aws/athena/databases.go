package athena

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// GetDatabase returns a database within a data catalog. A missing database is a
// ResourceNotFoundException (not InvalidRequestException), matching real
// Athena's metadata read path.
func (m *Mock) GetDatabase(_ context.Context, catalogName, databaseName string) (*driver.Database, error) {
	if catalogName == "" {
		catalogName = driver.DefaultDataCatalog
	}

	db, ok := m.databases.Get(databaseKey(catalogName, databaseName))
	if !ok {
		return nil, resourceNotFound("Database %s not found in catalog %s", databaseName, catalogName)
	}

	out := copyDatabase(db)

	return &out, nil
}

// ListDatabases returns the databases in a data catalog, sorted by name.
func (m *Mock) ListDatabases(_ context.Context, catalogName string, page driver.Pagination) ([]driver.Database, string, error) {
	if catalogName == "" {
		catalogName = driver.DefaultDataCatalog
	}

	prefix := catalogName + keySep
	keys := sortedKeys(m.databases.Keys())
	all := make([]driver.Database, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		db, ok := m.databases.Get(key)
		if !ok {
			continue
		}

		all = append(all, copyDatabase(db))
	}

	return paginate(all, page)
}

// GetDataCatalog returns a registered data catalog by name.
func (m *Mock) GetDataCatalog(_ context.Context, name string) (*driver.DataCatalog, error) {
	if name == "" {
		name = driver.DefaultDataCatalog
	}

	dc, ok := m.dataCatalogs.Get(name)
	if !ok {
		return nil, resourceNotFound("DataCatalog %s not found", name)
	}

	out := copyDataCatalog(dc)

	return &out, nil
}

// ListDataCatalogs returns the registered data-catalog summaries, sorted by
// name.
func (m *Mock) ListDataCatalogs(_ context.Context, page driver.Pagination) ([]driver.DataCatalogSummary, string, error) {
	names := sortedKeys(m.dataCatalogs.Keys())
	all := make([]driver.DataCatalogSummary, 0, len(names))

	for _, name := range names {
		dc, ok := m.dataCatalogs.Get(name)
		if !ok {
			continue
		}

		all = append(all, driver.DataCatalogSummary{CatalogName: dc.Name, Type: dc.Type})
	}

	return paginate(all, page)
}
