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

	conds, err := parseFilter(opts.Filter)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

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

// errUnsupportedFilter marks an OData $filter cloudemu can't evaluate. Rather
// than silently matching everything (a data-correctness hazard — a client that
// asked for a subset would get the whole table), QueryEntities surfaces it so
// the handler returns 400 InvalidInput.
var errUnsupportedFilter = errors.New(errors.InvalidArgument,
	"unsupported $filter: only \"Prop eq 'value'\" clauses joined by \"and\" are supported")

// parseFilter extracts equality conditions from an OData $filter of the form
// "Prop eq 'val' and Prop2 eq 'val2'". Only eq clauses joined by "and" are
// supported; any other operator (ne/gt/ge/lt/le/or, grouping, functions) —
// or a value literal containing " and "/" or " — returns errUnsupportedFilter
// rather than degrading to match-all.
func parseFilter(filter string) ([]eqCond, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}

	if hasUnsupportedFilterToken(filter) {
		return nil, errUnsupportedFilter
	}

	var conds []eqCond

	for _, clause := range strings.Split(filter, " and ") {
		prop, val, ok := parseEqClause(clause)
		if !ok {
			return nil, errUnsupportedFilter
		}

		conds = append(conds, eqCond{prop: prop, val: val})
	}

	return conds, nil
}

// hasUnsupportedFilterToken reports whether the filter uses an operator or
// construct beyond eq/and. Tokens are matched space-delimited so they don't
// trip on substrings inside identifiers or quoted values (e.g. "coordinator").
func hasUnsupportedFilterToken(filter string) bool {
	padded := " " + strings.ToLower(filter) + " "
	for _, tok := range []string{" or ", " ne ", " gt ", " ge ", " lt ", " le ", " not ", " and and "} {
		if strings.Contains(padded, tok) {
			return true
		}
	}

	return strings.ContainsAny(filter, "()")
}

// parseEqClause parses one "Prop eq 'value'" clause. The value may be a quoted
// string literal (which can contain spaces) or a bare token; a leftover quote
// or a non-eq operator makes it unsupported. Runs of whitespace between the
// property, operator, and value are tolerated.
func parseEqClause(clause string) (prop, val string, ok bool) {
	// property = first token; then the operator; then the (possibly quoted,
	// space-containing) value is whatever remains. Splitting token-by-token
	// with TrimSpace tolerates extra spaces that a fixed SplitN would not.
	prop, rest, found := cutToken(strings.TrimSpace(clause))
	if !found {
		return "", "", false
	}

	op, raw, found := cutToken(rest)
	if !found || !strings.EqualFold(op, "eq") {
		return "", "", false
	}

	if strings.ContainsAny(prop, "'()") {
		return "", "", false
	}

	// A quoted literal must be well-formed: a lone or unbalanced quote means we
	// split through a value (e.g. it contained " and ") — reject it.
	if strings.HasPrefix(raw, "'") && !(len(raw) >= 2 && strings.HasSuffix(raw, "'")) {
		return "", "", false
	}

	return prop, unquote(raw), true
}

// cutToken splits the first whitespace-delimited token off s, returning it and
// the trimmed remainder. found is false when s has no token or no remainder.
func cutToken(s string) (token, rest string, found bool) {
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return "", "", false
	}

	return s[:i], strings.TrimSpace(s[i+1:]), true
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
