package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// catalogData is a catalog resource plus its own lock.
type catalogData struct {
	cat driver.Catalog
	mu  sync.RWMutex
}

// CreateCatalog creates a Data Catalog catalog, atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateCatalog(_ context.Context, c driver.Catalog) error {
	if !validName(c.Name) {
		return invalidInput("catalog name %q is invalid", c.Name)
	}

	id := c.CatalogID
	if id == "" {
		id = c.Name
	}

	c.CatalogID = id
	c.CreateTime = m.now()
	c.UpdateTime = c.CreateTime

	if !m.catalogs.SetIfAbsent(id, &catalogData{cat: c}) {
		return alreadyExists("Catalog already exists: %s", id)
	}

	return nil
}

func (m *Mock) getCatalogData(catalogID string) (*catalogData, error) {
	if catalogID == "" {
		return nil, invalidInput("catalog id must not be empty")
	}

	cd, ok := m.catalogs.Get(catalogID)
	if !ok {
		return nil, entityNotFound("Catalog not found: %s", catalogID)
	}

	return cd, nil
}

// GetCatalog returns a deep copy of a catalog.
func (m *Mock) GetCatalog(_ context.Context, catalogID string) (*driver.Catalog, error) {
	cd, err := m.getCatalogData(catalogID)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := cd.cat

	return &out, nil
}

// UpdateCatalog replaces a catalog's mutable fields.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateCatalog(_ context.Context, catalogID string, c driver.Catalog) error {
	cd, err := m.getCatalogData(catalogID)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	created := cd.cat.CreateTime
	cd.cat = c
	cd.cat.CatalogID = catalogID
	cd.cat.CreateTime = created
	cd.cat.UpdateTime = m.now()

	return nil
}

// DeleteCatalog removes a catalog.
func (m *Mock) DeleteCatalog(_ context.Context, catalogID string) error {
	if _, err := m.getCatalogData(catalogID); err != nil {
		return err
	}

	m.catalogs.Delete(catalogID)

	return nil
}

// GetCatalogs lists catalogs with pagination.
func (m *Mock) GetCatalogs(_ context.Context, page driver.TablePagination) ([]driver.Catalog, string, error) {
	keys := sortedKeys(m.catalogs.Keys())
	all := make([]driver.Catalog, 0, len(keys))

	for _, key := range keys {
		cd, ok := m.catalogs.Get(key)
		if !ok {
			continue
		}

		cd.mu.RLock()
		all = append(all, cd.cat)
		cd.mu.RUnlock()
	}

	return paginate(all, page)
}
