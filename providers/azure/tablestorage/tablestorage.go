// Package tablestorage provides an in-memory mock implementation of the
// Azure Table Storage entity store, satisfying tablestorage/driver.TableStorage.
package tablestorage

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

// Compile-time check that Mock implements driver.TableStorage.
var _ driver.TableStorage = (*Mock)(nil)

// timestampProp and etagProp are the reserved system properties Table Storage
// injects on read. They are never persisted from a caller-supplied entity.
const (
	timestampProp = "Timestamp"
	etagProp      = "odata.etag"

	// numSystemProps is how many system properties render adds (Timestamp, etag).
	numSystemProps = 2
)

// storedEntity is one row plus its server-maintained system properties.
type storedEntity struct {
	props     driver.Entity
	timestamp time.Time
	etag      string
}

// tableData holds one table's entities, keyed by "partitionKey\x00rowKey".
type tableData struct {
	mu       sync.RWMutex
	entities map[string]*storedEntity
}

// Mock is an in-memory Table Storage backend.
type Mock struct {
	mu     sync.RWMutex
	tables map[string]*tableData
	opts   *config.Options

	tsMu   sync.Mutex
	lastTS time.Time
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

// nextTimestamp returns a strictly increasing timestamp so every mutation
// produces a fresh, stable ETag even when the configured clock does not
// advance between calls (e.g. FakeClock in tests).
func (m *Mock) nextTimestamp() time.Time {
	m.tsMu.Lock()
	defer m.tsMu.Unlock()

	now := m.opts.Clock.Now().UTC()
	if !now.After(m.lastTS) {
		now = m.lastTS.Add(time.Nanosecond)
	}

	m.lastTS = now

	return now
}

func makeETag(ts time.Time) string {
	return fmt.Sprintf("W/\"datetime'%s'\"", ts.Format(time.RFC3339Nano))
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

	m.tables[name] = &tableData{entities: make(map[string]*storedEntity)}

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

	sort.Strings(names)

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
func (m *Mock) InsertEntity(
	_ context.Context, table, partitionKey, rowKey string, entity driver.Entity,
) (string, error) {
	if partitionKey == "" || rowKey == "" {
		return "", errors.New(errors.InvalidArgument, "PartitionKey and RowKey are required")
	}

	td, err := m.table(table)
	if err != nil {
		return "", err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	return m.insertLocked(td, partitionKey, rowKey, entity)
}

// insertLocked inserts one entity; td.mu must be held.
func (m *Mock) insertLocked(td *tableData, partitionKey, rowKey string, entity driver.Entity) (string, error) {
	key := entityKey(partitionKey, rowKey)
	if _, ok := td.entities[key]; ok {
		return "", errors.Newf(errors.AlreadyExists, "entity (%q,%q) already exists", partitionKey, rowKey)
	}

	ts := m.nextTimestamp()
	se := &storedEntity{props: sanitize(entity), timestamp: ts, etag: makeETag(ts)}
	td.entities[key] = se

	return se.etag, nil
}

// GetEntity returns the entity addressed by partitionKey/rowKey, including its
// system Timestamp and odata.etag properties.
func (m *Mock) GetEntity(_ context.Context, table, partitionKey, rowKey string) (driver.Entity, error) {
	td, err := m.table(table)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	se, ok := td.entities[entityKey(partitionKey, rowKey)]
	if !ok {
		return nil, errors.Newf(errors.NotFound, "entity (%q,%q) not found", partitionKey, rowKey)
	}

	return se.render(), nil
}

// UpdateEntity merges or replaces an existing entity, honoring the If-Match
// precondition (empty or "*" is unconditional; a specific ETag must match).
func (m *Mock) UpdateEntity(
	_ context.Context, table, partitionKey, rowKey string, entity driver.Entity, mode driver.UpdateMode, ifMatch string,
) (string, error) {
	td, err := m.table(table)
	if err != nil {
		return "", err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	return m.updateLocked(td, partitionKey, rowKey, entity, mode, ifMatch)
}

// updateLocked updates one existing entity; td.mu must be held.
func (m *Mock) updateLocked(
	td *tableData, partitionKey, rowKey string, entity driver.Entity, mode driver.UpdateMode, ifMatch string,
) (string, error) {
	key := entityKey(partitionKey, rowKey)

	existing, ok := td.entities[key]
	if !ok {
		return "", errors.Newf(errors.NotFound, "entity (%q,%q) not found", partitionKey, rowKey)
	}

	if err := checkIfMatch(existing.etag, ifMatch); err != nil {
		return "", err
	}

	ts := m.nextTimestamp()

	if mode == driver.UpdateModeReplace {
		existing.props = sanitize(entity)
	} else {
		for k, v := range sanitize(entity) {
			existing.props[k] = v
		}
	}

	existing.timestamp = ts
	existing.etag = makeETag(ts)

	return existing.etag, nil
}

// DeleteEntity removes an entity, honoring the If-Match precondition.
func (m *Mock) DeleteEntity(_ context.Context, table, partitionKey, rowKey, ifMatch string) error {
	td, err := m.table(table)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	return deleteLocked(td, partitionKey, rowKey, ifMatch)
}

// deleteLocked deletes one entity; td.mu must be held.
func deleteLocked(td *tableData, partitionKey, rowKey, ifMatch string) error {
	key := entityKey(partitionKey, rowKey)

	existing, ok := td.entities[key]
	if !ok {
		return errors.Newf(errors.NotFound, "entity (%q,%q) not found", partitionKey, rowKey)
	}

	if err := checkIfMatch(existing.etag, ifMatch); err != nil {
		return err
	}

	delete(td.entities, key)

	return nil
}

// checkIfMatch enforces an If-Match precondition. An empty or "*" value matches
// unconditionally; any other value must equal the current ETag or the operation
// fails with 412 UpdateConditionNotSatisfied.
func checkIfMatch(currentETag, ifMatch string) error {
	if ifMatch == "" || ifMatch == driver.MatchAny {
		return nil
	}

	if ifMatch != currentETag {
		return errors.New(errors.FailedPrecondition, "the update condition specified in the request was not satisfied")
	}

	return nil
}

// render returns a caller-owned copy of the entity with system properties.
func (se *storedEntity) render() driver.Entity {
	out := make(driver.Entity, len(se.props)+numSystemProps)
	for k, v := range se.props {
		out[k] = v
	}

	out[timestampProp] = se.timestamp.Format(time.RFC3339Nano)
	out[etagProp] = se.etag

	return out
}

// sanitize copies caller properties, dropping reserved system keys so they can
// never be spoofed into the store.
func sanitize(e driver.Entity) driver.Entity {
	out := make(driver.Entity, len(e))

	for k, v := range e {
		switch k {
		case timestampProp, etagProp, "odata.metadata", "odata.editLink", "odata.id", "odata.type":
			continue
		default:
			out[k] = v
		}
	}

	return out
}

// QueryEntities returns a page of entities matching opts, ordered by
// (PartitionKey, RowKey), honoring the OData $filter, a partition restriction,
// $top, and a continuation position.
func (m *Mock) QueryEntities(
	_ context.Context, table string, opts driver.QueryOptions,
) (driver.QueryResult, error) {
	td, err := m.table(table)
	if err != nil {
		return driver.QueryResult{}, err
	}

	pred, err := parseFilter(opts.Filter)
	if err != nil {
		return driver.QueryResult{}, err
	}

	td.mu.RLock()
	ordered := td.sortedEntities()
	td.mu.RUnlock()

	return page(ordered, opts, pred), nil
}

// sortedEntities returns the table's entities ordered by (PartitionKey,
// RowKey). td.mu must be held.
func (td *tableData) sortedEntities() []*storedEntity {
	out := make([]*storedEntity, 0, len(td.entities))
	for _, se := range td.entities {
		out = append(out, se)
	}

	sort.Slice(out, func(i, j int) bool {
		pi, pj := asString(out[i].props["PartitionKey"]), asString(out[j].props["PartitionKey"])
		if pi != pj {
			return pi < pj
		}

		return asString(out[i].props["RowKey"]) < asString(out[j].props["RowKey"])
	})

	return out
}

// page applies partition restriction, filter, continuation and $top to the
// ordered entities, returning one result page.
func page(ordered []*storedEntity, opts driver.QueryOptions, pred predicate) driver.QueryResult {
	var res driver.QueryResult

	for _, se := range ordered {
		pk := asString(se.props["PartitionKey"])
		rk := asString(se.props["RowKey"])

		if opts.PartitionKey != "" && pk != opts.PartitionKey {
			continue
		}

		if !afterContinuation(pk, rk, opts.NextPartitionKey, opts.NextRowKey) {
			continue
		}

		if pred != nil && !pred(se.render()) {
			continue
		}

		if opts.Top > 0 && len(res.Entities) == opts.Top {
			res.NextPartitionKey = pk
			res.NextRowKey = rk

			return res
		}

		res.Entities = append(res.Entities, se.render())
	}

	return res
}

// afterContinuation reports whether (pk, rk) sorts at or after the continuation
// position. An empty continuation admits everything.
func afterContinuation(pk, rk, nextPK, nextRK string) bool {
	if nextPK == "" && nextRK == "" {
		return true
	}

	if pk != nextPK {
		return pk > nextPK
	}

	return rk >= nextRK
}

// ApplyBatch atomically applies an entity group transaction.
func (m *Mock) ApplyBatch(_ context.Context, table string, ops []driver.BatchOp) ([]driver.BatchResult, error) {
	td, err := m.table(table)
	if err != nil {
		return nil, err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	// Snapshot for rollback: shallow-copy the map and deep-copy touched rows.
	snapshot := make(map[string]*storedEntity, len(td.entities))
	for k, v := range td.entities {
		snapshot[k] = v.clone()
	}

	results := make([]driver.BatchResult, len(ops))

	for i := range ops {
		etag, opErr := m.applyOp(td, &ops[i])
		if opErr != nil {
			td.entities = snapshot

			return nil, &driver.BatchError{Index: i, Err: opErr}
		}

		results[i] = driver.BatchResult{ETag: etag}
	}

	return results, nil
}

// applyOp applies a single batch op; td.mu must be held.
func (m *Mock) applyOp(td *tableData, op *driver.BatchOp) (string, error) {
	switch op.Type {
	case driver.BatchInsert:
		return m.insertLocked(td, op.PartitionKey, op.RowKey, op.Entity)
	case driver.BatchUpsertReplace:
		return m.upsertLocked(td, op, driver.UpdateModeReplace)
	case driver.BatchUpsertMerge:
		return m.upsertLocked(td, op, driver.UpdateModeMerge)
	case driver.BatchUpdateReplace:
		return m.updateLocked(td, op.PartitionKey, op.RowKey, op.Entity, driver.UpdateModeReplace, op.IfMatch)
	case driver.BatchUpdateMerge:
		return m.updateLocked(td, op.PartitionKey, op.RowKey, op.Entity, driver.UpdateModeMerge, op.IfMatch)
	case driver.BatchDelete:
		return "", deleteLocked(td, op.PartitionKey, op.RowKey, op.IfMatch)
	default:
		return "", errors.New(errors.InvalidArgument, "unsupported batch operation")
	}
}

// upsertLocked inserts the entity if absent, otherwise updates it; td.mu held.
func (m *Mock) upsertLocked(td *tableData, op *driver.BatchOp, mode driver.UpdateMode) (string, error) {
	if _, ok := td.entities[entityKey(op.PartitionKey, op.RowKey)]; !ok {
		return m.insertLocked(td, op.PartitionKey, op.RowKey, op.Entity)
	}

	return m.updateLocked(td, op.PartitionKey, op.RowKey, op.Entity, mode, "")
}

func (se *storedEntity) clone() *storedEntity {
	props := make(driver.Entity, len(se.props))
	for k, v := range se.props {
		props[k] = v
	}

	return &storedEntity{props: props, timestamp: se.timestamp, etag: se.etag}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
