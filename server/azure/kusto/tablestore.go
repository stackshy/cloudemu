package kusto

import (
	"sort"
	"strings"
	"sync"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/azure/kusto/kql"
)

// dataStore holds the Kusto query data-plane state: the tables ingested and
// queried through the /v1|v2/rest endpoints, scoped per (cluster, database).
// It is independent of the ARM control-plane store — a real Kusto client often
// only touches the data plane in tests, and CloudEmu resolves the cluster from
// the request Host (with a default fallback), which does not carry the ARM
// subscription/resource-group scope. Each (cluster, database) pair auto-vivifies
// a tableStore on first use.
type dataStore struct {
	mu     sync.Mutex
	stores map[string]*tableStore
}

func newDataStore() *dataStore {
	return &dataStore{stores: map[string]*tableStore{}}
}

// storeFor returns the tableStore for a (cluster, database) pair, creating it on
// first use. Cluster and database names are case-insensitive in Kusto.
func (d *dataStore) storeFor(cluster, database string) *tableStore {
	key := strings.ToLower(cluster) + "\x00" + strings.ToLower(database)

	d.mu.Lock()
	defer d.mu.Unlock()

	ts, ok := d.stores[key]
	if !ok {
		ts = newTableStore()
		d.stores[key] = ts
	}

	return ts
}

// tableStore is the in-memory table set of a single database, keyed by
// lower-cased table name. It is its own concurrency unit.
type tableStore struct {
	mu     sync.RWMutex
	tables map[string]*kql.Table
}

func newTableStore() *tableStore {
	return &tableStore{tables: map[string]*kql.Table{}}
}

// tableKey normalises a table name to its store key; Kusto table names are
// case-insensitive within a database.
func tableKey(name string) string { return strings.ToLower(name) }

// createTable registers a new table schema. merge=false errors if the table
// already exists (.create table); merge=true is create-or-keep (.create-merge
// table), returning the existing table unchanged when the name is taken.
func (s *tableStore) createTable(name string, cols []kql.Column, merge bool) (*kql.Table, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.tables[tableKey(name)]; ok {
		if merge {
			return existing, nil
		}

		return nil, cerrors.Newf(cerrors.AlreadyExists, "table already exists: %s", name)
	}

	t := &kql.Table{Name: name, Columns: cols, Rows: [][]any{}}
	s.tables[tableKey(name)] = t

	return t, nil
}

// dropTable removes a table. A drop of a missing table errors unless ifExists is
// set, matching ".drop table T ifexists".
func (s *tableStore) dropTable(name string, ifExists bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tables[tableKey(name)]; !ok {
		if ifExists {
			return nil
		}

		return cerrors.Newf(cerrors.NotFound, "table not found: %s", name)
	}

	delete(s.tables, tableKey(name))

	return nil
}

// getTable returns a table by name.
func (s *tableStore) getTable(name string) (*kql.Table, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.tables[tableKey(name)]

	return t, ok
}

// listTables returns every table, ordered by name, for .show tables and
// .show database schema.
func (s *tableStore) listTables() []*kql.Table {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.tables))
	for k := range s.tables {
		names = append(names, k)
	}

	sort.Strings(names)

	out := make([]*kql.Table, 0, len(names))
	for _, k := range names {
		out = append(out, s.tables[k])
	}

	return out
}
