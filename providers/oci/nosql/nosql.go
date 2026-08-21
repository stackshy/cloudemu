// Package nosql provides an in-memory mock implementation of OCI NoSQL
// Database Cloud Service. It implements the portable database driver: an OCI
// table is the DynamoDB table, its shard key the partition key and the second
// primary key column the sort key.
//
// OCI is DDL-driven, so the OCI-shaped entry points take a SQL statement and
// derive the schema from it; the OCI-only value types they return live here
// and the capability interfaces consuming them live in server/oci/nosql.
package nosql

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time check that Mock implements the portable driver. The OCI-shaped
// capabilities live in server/oci/nosql and are checked there.
var _ driver.Database = (*Mock)(nil)

const timeFormat = time.RFC3339

// typeTable is the OCID resource segment for a NoSQL table.
const typeTable = "nosqltable"

// metricNamespace is the namespace real OCI NoSQL publishes table metrics in.
const metricNamespace = "oci_nosql"

// Table and index lifecycle states.
const (
	StateActive   = "ACTIVE"
	StateCreating = "CREATING"
	StateDeleting = "DELETING"
	StateUpdating = "UPDATING"
)

// Capacity modes a table's limits are expressed in.
const (
	CapacityProvisioned = "PROVISIONED"
	CapacityOnDemand    = "ON_DEMAND"
)

// ttlExpiryColumn carries the row expiry OCI's table-level TTL implies. The
// DDL grammar forbids a leading underscore in a column name, so it cannot
// collide with a declared column, and every read path strips it.
const ttlExpiryColumn = "_ttlExpiration"

// defaultPageLimit is the page size a query or scan falls back to.
const defaultPageLimit = 100

// TableLimits is OCI's throughput and storage allocation for a table.
type TableLimits struct {
	MaxReadUnits    int
	MaxWriteUnits   int
	MaxStorageInGBs int
	CapacityMode    string
}

// Column is one column of a table schema, as declared in the DDL.
type Column struct {
	Name         string
	Type         string
	IsNullable   bool
	DefaultValue string
}

// TTL is OCI's table-level row expiry in days, set by USING TTL in the DDL.
// OCI reports a table's TTL in days, so the DDL's HOURS unit is refused
// rather than rounded into a value the schema could not report back.
type TTL struct {
	Days int
}

// Schema is what the create-table DDL declares. ShardKey and PrimaryKey are
// the full OCI key lists; the portable projection takes the shard column as
// the partition key and the remaining primary key column as the sort key.
type Schema struct {
	Columns    []Column
	PrimaryKey []string
	ShardKey   []string
	TTL        TTL
}

// IndexKey is one column an index is built on.
type IndexKey struct {
	ColumnName string
}

// Index is a secondary index on a table.
type Index struct {
	Name           string
	Keys           []IndexKey
	LifecycleState string
}

// Table is an OCI NoSQL table.
type Table struct {
	ID                string
	Name              string
	CompartmentID     string
	DDLStatement      string
	Schema            Schema
	Limits            TableLimits
	LifecycleState    string
	TimeCreated       string
	TimeUpdated       string
	IsAutoReclaimable bool
	FreeformTags      map[string]string
}

// TableSpec describes a table to create over the OCI surface.
type TableSpec struct {
	CompartmentID     string
	DDLStatement      string
	Limits            TableLimits
	IsAutoReclaimable bool
	FreeformTags      map[string]string
}

// TableUpdate carries the mutable fields of UpdateTable. A nil field leaves
// the stored value alone.
type TableUpdate struct {
	DDLStatement      string
	Limits            *TableLimits
	IsAutoReclaimable *bool
	FreeformTags      map[string]string
}

// Row is a stored row plus the metadata OCI reports alongside its value.
type Row struct {
	Value            map[string]any
	TimeOfExpiration string
}

// tableData is a table and the rows it holds.
type tableData struct {
	ID                string
	Name              string
	DDLStatement      string
	Schema            Schema
	Limits            TableLimits
	LifecycleState    string
	TimeCreated       string
	TimeUpdated       string
	IsAutoReclaimable bool
	Scope             scope.Scope
	Tags              map[string]string
	Indexes           []*Index
	// ttl is the portable attribute-based TTL, kept apart from the schema's
	// table-level one so a portable caller and the DDL cannot overwrite
	// each other. A row expires when either says so.
	ttl   driver.TTLConfig
	items *memstore.Store[map[string]any]
}

// Mock is an in-memory mock implementation of OCI NoSQL Database.
type Mock struct {
	// mu guards the fields of stored tables and spans the reads and writes a
	// single operation makes: each store locks its own map, but the table
	// pointers it hands back are mutated in place, and the OCI entry points
	// resolve a name or OCID before touching the rows behind it.
	mu sync.RWMutex

	tables *memstore.Store[*tableData]
	// names maps a table OCID onto its name, so OCI callers can address a
	// table either way.
	names      *memstore.Store[string]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new OCI NoSQL mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		tables: memstore.New[*tableData](),
		names:  memstore.New[string](),
		opts:   opts,
	}
}

// SetMonitoring points the mock at the monitoring service, which it publishes
// read and write unit consumption to.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(name string, value float64, table string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: metricNamespace, MetricName: name, Value: value, Unit: "Count",
		Dimensions: map[string]string{"tableName": table}, Timestamp: m.opts.Clock.Now(),
	}})
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// lookup returns a table by name. Callers must hold m.mu.
func (m *Mock) lookup(name string) (*tableData, error) {
	t, ok := m.tables.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	return t, nil
}

// resolve returns a table addressed by either its name or its OCID, which is
// what OCI's tableNameOrId path parameter accepts. Callers must hold m.mu.
func (m *Mock) resolve(nameOrID string) (*tableData, error) {
	if name, ok := m.names.Get(nameOrID); ok {
		return m.lookup(name)
	}

	return m.lookup(nameOrID)
}

// itemKey is a row's identity: the shard key, then the sort key when the
// primary key declares one.
func itemKey(t *tableData, item map[string]any) string {
	pk := t.Schema.ShardKey[0]
	key := fmt.Sprintf("%v", item[pk])

	if sk := sortKeyOf(&t.Schema); sk != "" {
		key += ":" + fmt.Sprintf("%v", item[sk])
	}

	return key
}

// sortKeyOf returns the primary key column following the shard key, or the
// empty string for a single-column primary key.
func sortKeyOf(s *Schema) string {
	if len(s.PrimaryKey) < 2 { //nolint:mnd // a sort key is the second primary key column
		return ""
	}

	return s.PrimaryKey[1]
}

// toTableConfig projects a table onto the portable shape.
func toTableConfig(t *tableData) driver.TableConfig {
	cfg := driver.TableConfig{
		Name:         t.Name,
		PartitionKey: t.Schema.ShardKey[0],
		SortKey:      sortKeyOf(&t.Schema),
	}

	for _, idx := range t.Indexes {
		cfg.GSIs = append(cfg.GSIs, toGSIConfig(idx))
	}

	return cfg
}

// toGSIConfig projects an index onto the portable shape. An index on more
// than two columns keeps its full key list on the OCI side; the portable
// projection has room for a partition and a sort key only.
func toGSIConfig(idx *Index) driver.GSIConfig {
	cfg := driver.GSIConfig{Name: idx.Name}

	if len(idx.Keys) > 0 {
		cfg.PartitionKey = idx.Keys[0].ColumnName
	}

	if len(idx.Keys) > 1 {
		cfg.SortKey = idx.Keys[1].ColumnName
	}

	return cfg
}

// toTable projects a table onto the OCI shape.
func toTable(t *tableData) Table {
	return Table{
		ID:                t.ID,
		Name:              t.Name,
		CompartmentID:     t.Scope.Compartment,
		DDLStatement:      t.DDLStatement,
		Schema:            cloneSchema(&t.Schema),
		Limits:            t.Limits,
		LifecycleState:    t.LifecycleState,
		TimeCreated:       t.TimeCreated,
		TimeUpdated:       t.TimeUpdated,
		IsAutoReclaimable: t.IsAutoReclaimable,
		FreeformTags:      maps.Clone(t.Tags),
	}
}

func cloneSchema(s *Schema) Schema {
	out := *s
	out.Columns = append([]Column(nil), s.Columns...)
	out.PrimaryKey = append([]string(nil), s.PrimaryKey...)
	out.ShardKey = append([]string(nil), s.ShardKey...)

	return out
}

// visible copies an item without the internal expiry column, which is
// bookkeeping rather than a declared column.
func visible(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))

	for k, v := range item {
		if k == ttlExpiryColumn {
			continue
		}

		out[k] = v
	}

	return out
}

// expired reports whether a row has passed either TTL: the table-level one the
// DDL sets, or the attribute-based one a portable caller configures.
func (m *Mock) expired(t *tableData, item map[string]any) bool {
	now := m.opts.Clock.Now().Unix()

	if exp, ok := toUnix(item[ttlExpiryColumn]); ok && exp > 0 && now > exp {
		return true
	}

	if !t.ttl.Enabled || t.ttl.AttributeName == "" {
		return false
	}

	exp, ok := toUnix(item[t.ttl.AttributeName])

	return ok && exp > 0 && now > exp
}

func toUnix(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}

	return 0, false
}

// liveItems returns the table's unexpired rows, stripped of bookkeeping.
// Callers must hold m.mu.
func (m *Mock) liveItems(t *tableData) []map[string]any {
	all := t.items.All()
	out := make([]map[string]any, 0, len(all))

	for _, item := range all {
		if m.expired(t, item) {
			continue
		}

		out = append(out, visible(item))
	}

	return out
}

// CreateTable creates a table from the portable config. OCI is DDL-driven, so
// the equivalent statement is synthesized and reported by GetTable; every
// column takes OCI's STRING type, which is all the portable shape declares.
func (m *Mock) CreateTable(_ context.Context, cfg driver.TableConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "table name is required")
	}

	if cfg.PartitionKey == "" {
		return cerrors.New(cerrors.InvalidArgument, "partition key is required")
	}

	schema := schemaFromConfig(&cfg)

	t, err := m.newTable(cfg.Name, ddlFromSchema(cfg.Name, &schema), &schema, defaultLimits())
	if err != nil {
		return err
	}

	for i := range cfg.GSIs {
		if _, err := m.addIndex(t, indexFromGSI(&cfg.GSIs[i])); err != nil {
			return err
		}
	}

	return nil
}

// defaultLimits is what a table created through the portable API gets: OCI
// requires limits, and on-demand is the mode that names no numbers.
func defaultLimits() TableLimits {
	return TableLimits{CapacityMode: CapacityOnDemand}
}

// newTable records a new table. Callers must hold m.mu.
func (m *Mock) newTable(name, ddl string, schema *Schema, limits TableLimits) (*tableData, error) {
	if m.tables.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "table %q already exists", name)
	}

	now := m.now()
	t := &tableData{
		ID:             idgen.OCID(typeTable, m.opts.Realm, m.opts.OCIRegion()),
		Name:           name,
		DDLStatement:   ddl,
		Schema:         *schema,
		Limits:         limits,
		LifecycleState: StateActive,
		TimeCreated:    now,
		TimeUpdated:    now,
		Scope:          scope.Scope{Compartment: m.opts.CompartmentID},
		items:          memstore.New[map[string]any](),
	}

	m.tables.Set(name, t)
	m.names.Set(t.ID, name)

	return t, nil
}

// DeleteTable deletes a table and the rows in it.
func (m *Mock) DeleteTable(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.dropTable(name)
}

// dropTable removes a table by name. Callers must hold m.mu.
func (m *Mock) dropTable(name string) error {
	t, err := m.lookup(name)
	if err != nil {
		return err
	}

	m.tables.Delete(name)
	m.names.Delete(t.ID)

	return nil
}

// DescribeTable returns the portable projection of a table.
func (m *Mock) DescribeTable(_ context.Context, name string) (*driver.TableConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.lookup(name)
	if err != nil {
		return nil, err
	}

	cfg := toTableConfig(t)

	return &cfg, nil
}

// ListTables returns every table name, ordered.
func (m *Mock) ListTables(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := m.tables.Keys()
	sort.Strings(names)

	return names, nil
}
