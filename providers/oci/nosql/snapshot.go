package nosql

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// nosqlSnapshot is the full serialized state of the OCI NoSQL mock. tables is
// dumped keyed by compartment and name, which is how it is addressed, and
// names by table OCID, so a table restores under both identities and the
// OCID cross-reference still resolves. The mutex, the *config.Options and the
// wired monitoring service are not serialized; the mock mints no lazy default
// or counter, so there is nothing else to carry.
type nosqlSnapshot struct {
	Tables json.RawMessage `json:"tables,omitempty"`
	Names  json.RawMessage `json:"names,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// NoSQL holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap nosqlSnapshot

	for _, d := range m.snapshotDumps(&snap) {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("nosql: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every table
// keeps its OCID, its compartment and the rows it held.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap nosqlSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("nosql: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.snapshotDumps(&snap) {
		if len(*d.dst) == 0 {
			continue
		}

		if err := d.load(*d.dst); err != nil {
			return fmt.Errorf("nosql: restore store: %w", err)
		}
	}

	return nil
}

// storeDump pairs a snapshot field with its store's dump and load functions, so
// Snapshot and Restore share one table and cannot drift apart.
type storeDump struct {
	dst  *json.RawMessage
	fn   func() ([]byte, error)
	load func([]byte) error
}

// snapshotDumps lists every store alongside the snapshot field it maps to.
func (m *Mock) snapshotDumps(snap *nosqlSnapshot) []storeDump {
	return []storeDump{
		{&snap.Tables, m.tables.Snapshot, m.tables.LoadSnapshot},
		{&snap.Names, m.names.Snapshot, m.names.LoadSnapshot},
	}
}

// tableDump is a table's serialized form. tableData's row store is a memstore,
// which JSON neither writes nor rebuilds, so the rows travel as a plain map and
// the store is remade from them on the way back; the attribute-based TTL and
// the compartment are unexported or nested for the same reason.
type tableDump struct {
	ID                string                    `json:"id"`
	Name              string                    `json:"name"`
	DDLStatement      string                    `json:"ddlStatement,omitempty"`
	Schema            Schema                    `json:"schema"`
	Limits            TableLimits               `json:"limits"`
	LifecycleState    string                    `json:"lifecycleState,omitempty"`
	TimeCreated       string                    `json:"timeCreated,omitempty"`
	TimeUpdated       string                    `json:"timeUpdated,omitempty"`
	IsAutoReclaimable bool                      `json:"isAutoReclaimable,omitempty"`
	Scope             scope.Scope               `json:"scope"`
	Tags              map[string]string         `json:"tags,omitempty"`
	Indexes           []*Index                  `json:"indexes,omitempty"`
	TTL               driver.TTLConfig          `json:"ttl"`
	Items             map[string]map[string]any `json:"items,omitempty"`
}

// MarshalJSON dumps a table and the rows it holds.
func (t *tableData) MarshalJSON() ([]byte, error) {
	return json.Marshal(tableDump{
		ID:                t.ID,
		Name:              t.Name,
		DDLStatement:      t.DDLStatement,
		Schema:            t.Schema,
		Limits:            t.Limits,
		LifecycleState:    t.LifecycleState,
		TimeCreated:       t.TimeCreated,
		TimeUpdated:       t.TimeUpdated,
		IsAutoReclaimable: t.IsAutoReclaimable,
		Scope:             t.Scope,
		Tags:              t.Tags,
		Indexes:           t.Indexes,
		TTL:               t.ttl,
		Items:             t.items.All(),
	})
}

// UnmarshalJSON rebuilds a table, remaking the row store the dump flattened.
// The store is always allocated, so a table restored from a dump that carried
// no rows is written to rather than nil-dereferenced.
func (t *tableData) UnmarshalJSON(data []byte) error {
	var d tableDump
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}

	*t = tableData{
		ID:                d.ID,
		Name:              d.Name,
		DDLStatement:      d.DDLStatement,
		Schema:            d.Schema,
		Limits:            d.Limits,
		LifecycleState:    d.LifecycleState,
		TimeCreated:       d.TimeCreated,
		TimeUpdated:       d.TimeUpdated,
		IsAutoReclaimable: d.IsAutoReclaimable,
		Scope:             d.Scope,
		Tags:              d.Tags,
		Indexes:           d.Indexes,
		ttl:               d.TTL,
		items:             memstore.New[map[string]any](),
	}

	for key, item := range d.Items {
		t.items.Set(key, retypeRow(&t.Schema, item))
	}

	return nil
}

// retypeRow fits a decoded row back to the types its columns declare. JSON has
// one number type, so an INTEGER or LONG column comes back as a float64 unless
// it is coerced again; a value that no longer fits its column is left as it
// decoded rather than dropped, so a restore never loses a row silently.
func retypeRow(s *Schema, item map[string]any) map[string]any {
	for i := range s.Columns {
		col := &s.Columns[i]

		v, ok := item[col.Name]
		if !ok || v == nil {
			continue
		}

		if typed, err := convertTyped(col, v); err == nil {
			item[col.Name] = typed
		}
	}

	// The row expiry is bookkeeping rather than a declared column, and is
	// compared as an integer.
	if exp, ok := toUnix(item[ttlExpiryColumn]); ok {
		item[ttlExpiryColumn] = exp
	}

	return item
}
