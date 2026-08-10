package glue

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// partitionData is a partition plus its own lock.
type partitionData struct {
	part driver.Partition
	mu   sync.RWMutex
}

// requireTable errors with EntityNotFound if the parent table is absent, so a
// partition can't attach to a non-existent table.
func (m *Mock) requireTable(cat, db, table string) error {
	if !m.tables.Has(nameKey(cat, db, table)) {
		return entityNotFound("Table not found: %s", table)
	}

	return nil
}

// CreatePartition adds a partition to a table, claiming its value-key atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreatePartition(_ context.Context, catalogID, dbName, tblName string, p driver.Partition) error {
	cat := m.catalogOrDefault(catalogID)

	if err := m.requireTable(cat, dbName, tblName); err != nil {
		return err
	}

	if len(p.Values) == 0 {
		return invalidInput("partition values must not be empty")
	}

	p.CatalogID = cat
	p.DatabaseName = dbName
	p.TableName = tblName
	p.CreationTime = m.now()
	stored := copyPartition(p)

	if !m.partitions.SetIfAbsent(partitionKey(cat, dbName, tblName, p.Values), &partitionData{part: stored}) {
		return alreadyExists("Partition already exists: %v", p.Values)
	}

	return nil
}

// GetPartition returns a deep copy of a partition by its values.
func (m *Mock) GetPartition(
	_ context.Context, catalogID, dbName, tblName string, values []string,
) (*driver.Partition, error) {
	cat := m.catalogOrDefault(catalogID)

	pd, ok := m.partitions.Get(partitionKey(cat, dbName, tblName, values))
	if !ok {
		return nil, entityNotFound("Partition not found: %v", values)
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	out := copyPartition(pd.part)

	return &out, nil
}

// UpdatePartition replaces a partition; a value change re-keys atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdatePartition(
	_ context.Context, catalogID, dbName, tblName string, oldValues []string, p driver.Partition,
) error {
	cat := m.catalogOrDefault(catalogID)
	oldKey := partitionKey(cat, dbName, tblName, oldValues)

	pd, ok := m.partitions.Get(oldKey)
	if !ok {
		return entityNotFound("Partition not found: %v", oldValues)
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	updated := copyPartition(p)
	updated.CatalogID = cat
	updated.DatabaseName = dbName
	updated.TableName = tblName
	updated.CreationTime = pd.part.CreationTime

	newKey := partitionKey(cat, dbName, tblName, p.Values)
	if newKey != oldKey {
		if !m.partitions.SetIfAbsent(newKey, &partitionData{part: updated}) {
			return alreadyExists("Partition already exists: %v", p.Values)
		}

		m.partitions.Delete(oldKey)

		return nil
	}

	pd.part = updated

	return nil
}

// DeletePartition removes a partition by its values.
func (m *Mock) DeletePartition(_ context.Context, catalogID, dbName, tblName string, values []string) error {
	cat := m.catalogOrDefault(catalogID)

	if !m.partitions.Delete(partitionKey(cat, dbName, tblName, values)) {
		return entityNotFound("Partition not found: %v", values)
	}

	return nil
}

// GetPartitions lists a table's partitions with pagination.
func (m *Mock) GetPartitions(
	_ context.Context, catalogID, dbName, tblName string, page driver.TablePagination,
) ([]driver.Partition, string, error) {
	cat := m.catalogOrDefault(catalogID)

	if err := m.requireTable(cat, dbName, tblName); err != nil {
		return nil, "", err
	}

	prefix := nameKey(cat, dbName, tblName) + keySep
	keys := sortedKeys(m.partitions.Keys())
	all := make([]driver.Partition, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		pd, ok := m.partitions.Get(key)
		if !ok {
			continue
		}

		pd.mu.RLock()
		all = append(all, copyPartition(pd.part))
		pd.mu.RUnlock()
	}

	return paginate(all, page)
}

// BatchCreatePartition creates several partitions, validating all values before
// any mutation so a bad entry can't leave a partial batch committed.
func (m *Mock) BatchCreatePartition(
	_ context.Context, catalogID, dbName, tblName string, ps []driver.Partition,
) ([]driver.BatchError, error) {
	cat := m.catalogOrDefault(catalogID)

	if err := m.requireTable(cat, dbName, tblName); err != nil {
		return nil, err
	}

	for i := range ps {
		if len(ps[i].Values) == 0 {
			return nil, invalidInput("partition values must not be empty")
		}
	}

	var errs []driver.BatchError

	for i := range ps {
		if err := m.CreatePartition(context.Background(), cat, dbName, tblName, ps[i]); err != nil {
			errs = append(errs, driver.BatchError{
				Values: copyStrings(ps[i].Values), ErrorCode: driver.ExAlreadyExists, ErrorMessage: err.Error(),
			})
		}
	}

	return errs, nil
}

// BatchDeletePartition deletes several partitions, collecting per-partition
// errors after validating every value list.
func (m *Mock) BatchDeletePartition(
	_ context.Context, catalogID, dbName, tblName string, values [][]string,
) ([]driver.BatchError, error) {
	for i := range values {
		if len(values[i]) == 0 {
			return nil, invalidInput("partition values must not be empty")
		}
	}

	var errs []driver.BatchError

	for i := range values {
		if err := m.DeletePartition(context.Background(), catalogID, dbName, tblName, values[i]); err != nil {
			errs = append(errs, driver.BatchError{
				Values: copyStrings(values[i]), ErrorCode: driver.ExEntityNotFound, ErrorMessage: err.Error(),
			})
		}
	}

	return errs, nil
}

// BatchUpdatePartition updates several partitions, validating every entry first.
func (m *Mock) BatchUpdatePartition(
	_ context.Context, catalogID, dbName, tblName string, entries []driver.BatchUpdatePartitionEntry,
) ([]driver.BatchError, error) {
	for i := range entries {
		if len(entries[i].PartitionValueList) == 0 || len(entries[i].Partition.Values) == 0 {
			return nil, invalidInput("partition values must not be empty")
		}
	}

	var errs []driver.BatchError

	for i := range entries {
		err := m.UpdatePartition(
			context.Background(), catalogID, dbName, tblName,
			entries[i].PartitionValueList, entries[i].Partition,
		)
		if err != nil {
			errs = append(errs, driver.BatchError{
				Values:    copyStrings(entries[i].PartitionValueList),
				ErrorCode: driver.ExEntityNotFound, ErrorMessage: err.Error(),
			})
		}
	}

	return errs, nil
}

// BatchGetPartition returns the found partitions and the value lists that had no
// matching partition (unprocessed), matching Glue's split response.
func (m *Mock) BatchGetPartition(
	_ context.Context, catalogID, dbName, tblName string, values [][]string,
) ([]driver.Partition, [][]string, error) {
	cat := m.catalogOrDefault(catalogID)

	if err := m.requireTable(cat, dbName, tblName); err != nil {
		return nil, nil, err
	}

	found := make([]driver.Partition, 0, len(values))

	var unprocessed [][]string

	for i := range values {
		p, err := m.GetPartition(context.Background(), cat, dbName, tblName, values[i])
		if err != nil {
			unprocessed = append(unprocessed, copyStrings(values[i]))

			continue
		}

		found = append(found, *p)
	}

	return found, unprocessed, nil
}
