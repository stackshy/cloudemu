// Package tablestorage provides an in-memory mock implementation of the
// Azure Table Storage entity store, satisfying tablestorage/driver.TableStorage.
package tablestorage

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

// Compile-time check that Mock implements driver.TableStorage.
var _ driver.TableStorage = (*Mock)(nil)

// tableData holds one table's entities, keyed by "partitionKey\x00rowKey".
type tableData struct {
	mu       sync.RWMutex
	entities map[string]driver.Entity
}

// Mock is an in-memory Table Storage backend.
type Mock struct {
	mu     sync.RWMutex
	tables map[string]*tableData
	opts   *config.Options
}

// New creates a new Table Storage mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		tables: make(map[string]*tableData),
		opts:   opts,
	}
}

func entityKey(partitionKey, rowKey string) string {
	return partitionKey + "\x00" + rowKey
}

// CreateTable creates a new empty table.
func (m *Mock) CreateTable(_ context.Context, name string) error {
	if name == "" {
		return errors.New(errors.InvalidArgument, "table name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tables[name]; ok {
		return errors.Newf(errors.AlreadyExists, "table %q already exists", name)
	}

	m.tables[name] = &tableData{entities: make(map[string]driver.Entity)}

	return nil
}

// DeleteTable removes a table and all its entities.
func (m *Mock) DeleteTable(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tables[name]; !ok {
		return errors.Newf(errors.NotFound, "table %q not found", name)
	}

	delete(m.tables, name)

	return nil
}

// ListTables returns the names of all tables.
func (m *Mock) ListTables(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.tables))
	for name := range m.tables {
		names = append(names, name)
	}

	return names, nil
}

func (m *Mock) table(name string) (*tableData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, ok := m.tables[name]
	if !ok {
		return nil, errors.Newf(errors.NotFound, "table %q not found", name)
	}

	return td, nil
}

// InsertEntity adds a new entity. It fails if an entity with the same
// PartitionKey/RowKey already exists.
func (m *Mock) InsertEntity(_ context.Context, table, partitionKey, rowKey string, entity driver.Entity) error {
	if partitionKey == "" || rowKey == "" {
		return errors.New(errors.InvalidArgument, "PartitionKey and RowKey are required")
	}

	td, err := m.table(table)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	key := entityKey(partitionKey, rowKey)
	if _, ok := td.entities[key]; ok {
		return errors.Newf(errors.AlreadyExists, "entity (%q,%q) already exists", partitionKey, rowKey)
	}

	td.entities[key] = cloneEntity(entity)

	return nil
}

// GetEntity returns the entity addressed by partitionKey/rowKey.
func (m *Mock) GetEntity(_ context.Context, table, partitionKey, rowKey string) (driver.Entity, error) {
	td, err := m.table(table)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	ent, ok := td.entities[entityKey(partitionKey, rowKey)]
	if !ok {
		return nil, errors.Newf(errors.NotFound, "entity (%q,%q) not found", partitionKey, rowKey)
	}

	return cloneEntity(ent), nil
}

// UpdateEntity merges or replaces an existing entity.
func (m *Mock) UpdateEntity(
	_ context.Context, table, partitionKey, rowKey string, entity driver.Entity, mode driver.UpdateMode,
) error {
	td, err := m.table(table)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	key := entityKey(partitionKey, rowKey)

	existing, ok := td.entities[key]
	if !ok {
		return errors.Newf(errors.NotFound, "entity (%q,%q) not found", partitionKey, rowKey)
	}

	if mode == driver.UpdateModeReplace {
		td.entities[key] = cloneEntity(entity)
		return nil
	}

	merged := cloneEntity(existing)
	for k, v := range entity {
		merged[k] = v
	}

	td.entities[key] = merged

	return nil
}

// DeleteEntity removes an entity.
func (m *Mock) DeleteEntity(_ context.Context, table, partitionKey, rowKey string) error {
	td, err := m.table(table)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	key := entityKey(partitionKey, rowKey)
	if _, ok := td.entities[key]; !ok {
		return errors.Newf(errors.NotFound, "entity (%q,%q) not found", partitionKey, rowKey)
	}

	delete(td.entities, key)

	return nil
}

// QueryEntities returns entities matching the given options. Filtering
// supports a partition-key restriction plus a best-effort OData $filter parse
// (see matchesFilter); unrecognized filters match everything.
func (m *Mock) QueryEntities(_ context.Context, table string, opts driver.QueryOptions) ([]driver.Entity, error) {
	td, err := m.table(table)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	conds := parseFilter(opts.Filter)

	results := make([]driver.Entity, 0, len(td.entities))

	for _, ent := range td.entities {
		if opts.PartitionKey != "" && asString(ent["PartitionKey"]) != opts.PartitionKey {
			continue
		}

		if !matchesConds(ent, conds) {
			continue
		}

		results = append(results, cloneEntity(ent))
	}

	return results, nil
}

func cloneEntity(e driver.Entity) driver.Entity {
	out := make(driver.Entity, len(e))
	for k, v := range e {
		out[k] = v
	}

	return out
}

// eqCond is a single "property eq value" equality condition.
type eqCond struct {
	prop string
	val  string
}

// parseFilter extracts the equality conditions from a simple OData $filter of
// the form "Prop eq 'val' and Prop2 eq 'val2'". Anything it can't parse is
// dropped, so an unsupported filter degrades to "match all" rather than an
// error — adequate for the common query-by-partition case.
func parseFilter(filter string) []eqCond {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil
	}

	var conds []eqCond

	for _, clause := range strings.Split(filter, " and ") {
		fields := strings.Fields(strings.TrimSpace(clause))

		const eqParts = 3
		if len(fields) != eqParts || !strings.EqualFold(fields[1], "eq") {
			continue
		}

		conds = append(conds, eqCond{prop: fields[0], val: unquote(fields[2])})
	}

	return conds
}

func matchesConds(ent driver.Entity, conds []eqCond) bool {
	for _, c := range conds {
		if asString(ent[c.prop]) != c.val {
			return false
		}
	}

	return true
}

// unquote strips surrounding single quotes from an OData string literal.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}

	return s
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
