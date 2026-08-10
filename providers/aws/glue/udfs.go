package glue

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// udfData is a user-defined function plus its own lock.
type udfData struct {
	fn driver.UserDefinedFunction
	mu sync.RWMutex
}

// CreateUserDefinedFunction creates a UDF under a database, atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateUserDefinedFunction(
	_ context.Context, catalogID, dbName string, fn driver.UserDefinedFunction,
) error {
	cat := m.catalogOrDefault(catalogID)

	if !validName(fn.Name) {
		return invalidInput("function name %q is invalid", fn.Name)
	}

	if err := m.requireDatabase(cat, dbName); err != nil {
		return err
	}

	fn.CatalogID = cat
	fn.DatabaseName = dbName
	fn.CreateTime = m.now()
	stored := copyUDF(fn)

	if !m.udfs.SetIfAbsent(nameKey(cat, dbName, fn.Name), &udfData{fn: stored}) {
		return alreadyExists("Function already exists: %s", fn.Name)
	}

	return nil
}

func (m *Mock) getUDFData(catalogID, dbName, name string) (*udfData, string, error) {
	cat := m.catalogOrDefault(catalogID)

	if !validName(name) {
		return nil, cat, invalidInput("function name %q is invalid", name)
	}

	ud, ok := m.udfs.Get(nameKey(cat, dbName, name))
	if !ok {
		return nil, cat, entityNotFound("Function not found: %s", name)
	}

	return ud, cat, nil
}

// GetUserDefinedFunction returns a deep copy of a UDF.
func (m *Mock) GetUserDefinedFunction(
	_ context.Context, catalogID, dbName, name string,
) (*driver.UserDefinedFunction, error) {
	ud, _, err := m.getUDFData(catalogID, dbName, name)
	if err != nil {
		return nil, err
	}

	ud.mu.RLock()
	defer ud.mu.RUnlock()

	out := copyUDF(ud.fn)

	return &out, nil
}

// UpdateUserDefinedFunction replaces a UDF's mutable fields.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateUserDefinedFunction(
	_ context.Context, catalogID, dbName, name string, fn driver.UserDefinedFunction,
) error {
	ud, cat, err := m.getUDFData(catalogID, dbName, name)
	if err != nil {
		return err
	}

	ud.mu.Lock()
	defer ud.mu.Unlock()

	created := ud.fn.CreateTime
	ud.fn = copyUDF(fn)
	ud.fn.CatalogID = cat
	ud.fn.DatabaseName = dbName
	ud.fn.Name = name
	ud.fn.CreateTime = created

	return nil
}

// DeleteUserDefinedFunction removes a UDF.
func (m *Mock) DeleteUserDefinedFunction(_ context.Context, catalogID, dbName, name string) error {
	_, cat, err := m.getUDFData(catalogID, dbName, name)
	if err != nil {
		return err
	}

	m.udfs.Delete(nameKey(cat, dbName, name))

	return nil
}

// GetUserDefinedFunctions lists UDFs under a database with pagination.
func (m *Mock) GetUserDefinedFunctions(
	_ context.Context, catalogID, dbName string, page driver.TablePagination,
) ([]driver.UserDefinedFunction, string, error) {
	cat := m.catalogOrDefault(catalogID)
	prefix := nameKey(cat, dbName) + keySep

	keys := sortedKeys(m.udfs.Keys())
	all := make([]driver.UserDefinedFunction, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		ud, ok := m.udfs.Get(key)
		if !ok {
			continue
		}

		ud.mu.RLock()
		all = append(all, copyUDF(ud.fn))
		ud.mu.RUnlock()
	}

	return paginate(all, page)
}
