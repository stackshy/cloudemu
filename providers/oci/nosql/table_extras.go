package nosql

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// CreateOCITable creates a table from a CREATE TABLE statement. The compartment
// the caller names is recorded on the table and every list filters by it.
//
//nolint:gocritic // hugeParam: TableSpec mirrors OCI's CreateTableDetails and is passed by value like it.
func (m *Mock) CreateOCITable(_ context.Context, spec TableSpec) (*Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if spec.CompartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	d, err := ParseDDL(spec.DDLStatement)
	if err != nil {
		return nil, err
	}

	if d.Kind != DDLCreateTable {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "CreateTable takes a CREATE TABLE statement, got %s", d.Kind)
	}

	// CreateTableDetails names the table alongside the DDL; the two must agree
	// before anything, an existing table included, is touched.
	if spec.Name != "" && !strings.EqualFold(spec.Name, d.Table) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"name %s does not match the table named by ddlStatement, %s", spec.Name, d.Table)
	}

	limits, err := normaliseLimits(spec.Limits)
	if err != nil {
		return nil, err
	}

	if existing, ok := m.tables.Get(tableKey(spec.CompartmentID, d.Table)); ok {
		if d.IfNotExists {
			return ptr(toTable(existing)), nil
		}

		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"table %q already exists in compartment %q", d.Table, spec.CompartmentID)
	}

	t, err := m.newTable(spec.CompartmentID, d.Table, normaliseStatement(spec.DDLStatement), &d.Schema, limits)
	if err != nil {
		return nil, err
	}

	t.IsAutoReclaimable = spec.IsAutoReclaimable
	t.Tags = maps.Clone(spec.FreeformTags)

	return ptr(toTable(t)), nil
}

// normaliseLimits validates a table's capacity against its mode: an on-demand
// table sets no read or write units, a provisioned one must set both.
func normaliseLimits(l TableLimits) (TableLimits, error) {
	if l.CapacityMode == "" {
		l.CapacityMode = CapacityProvisioned
	}

	if l.MaxStorageInGBs < 0 {
		return l, cerrors.New(cerrors.InvalidArgument, "maxStorageInGBs must not be negative")
	}

	switch l.CapacityMode {
	case CapacityOnDemand:
		if l.MaxReadUnits != 0 || l.MaxWriteUnits != 0 {
			return l, cerrors.New(cerrors.InvalidArgument,
				"an ON_DEMAND table sets no maxReadUnits or maxWriteUnits")
		}

		return l, nil
	case CapacityProvisioned:
		if l.MaxReadUnits <= 0 || l.MaxWriteUnits <= 0 {
			return l, cerrors.New(cerrors.InvalidArgument,
				"a PROVISIONED table sets maxReadUnits and maxWriteUnits above zero")
		}

		if l.MaxStorageInGBs < 1 {
			return l, cerrors.New(cerrors.InvalidArgument, "a PROVISIONED table sets maxStorageInGBs to at least 1")
		}

		return l, nil
	}

	return l, cerrors.Newf(cerrors.InvalidArgument, "capacityMode %q is not ON_DEMAND or PROVISIONED", l.CapacityMode)
}

// GetOCITable returns a table by name or OCID.
func (m *Mock) GetOCITable(_ context.Context, compartmentID, nameOrID string) (*Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(compartmentID, nameOrID)
	if err != nil {
		return nil, err
	}

	return ptr(toTable(t)), nil
}

// ListOCITables returns the tables in a compartment, ordered by name. A
// non-empty name narrows the listing to that one table, as OCI's name query
// parameter does.
func (m *Mock) ListOCITables(_ context.Context, compartmentID, name string) ([]Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter := scope.Scope{Compartment: compartmentID}
	keys := m.tables.Keys()
	out := make([]Table, 0, len(keys))

	for _, k := range keys {
		t, ok := m.tables.Get(k)
		if !ok || !t.Scope.Matches(filter) {
			continue
		}

		if name != "" && !strings.EqualFold(t.Name, name) {
			continue
		}

		out = append(out, toTable(t))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// UpdateOCITable applies an ALTER TABLE statement, new limits, tags and the
// auto-reclaim flag. Every field is optional, as UpdateTable's are.
func (m *Mock) UpdateOCITable(_ context.Context, compartmentID, nameOrID string, upd TableUpdate) (*Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(compartmentID, nameOrID)
	if err != nil {
		return nil, err
	}

	// Stage every change and keep it only when all of them hold, so a refused
	// update leaves the table as it was.
	staged := *t
	staged.Schema = cloneSchema(&t.Schema)

	var added, dropped []string

	if upd.DDLStatement != "" {
		if added, dropped, err = applyAlter(&staged, upd.DDLStatement); err != nil {
			return nil, err
		}
	}

	if upd.Limits != nil {
		limits, err := normaliseLimits(*upd.Limits)
		if err != nil {
			return nil, err
		}

		staged.Limits = limits
	}

	t.Schema, t.DDLStatement, t.Limits = staged.Schema, staged.DDLStatement, staged.Limits
	stripColumns(t, dropped)
	fillColumns(t, added)

	if upd.IsAutoReclaimable != nil {
		t.IsAutoReclaimable = *upd.IsAutoReclaimable
	}

	if upd.FreeformTags != nil {
		t.Tags = maps.Clone(upd.FreeformTags)
	}

	t.TimeUpdated = m.now()

	return ptr(toTable(t)), nil
}

// applyAlter applies one ALTER TABLE statement to a table, returning the
// columns it added and dropped. Callers must hold m.mu.
func applyAlter(t *tableData, statement string) (added, dropped []string, err error) {
	d, err := ParseDDL(statement)
	if err != nil {
		return nil, nil, err
	}

	if d.Kind != DDLAlterTable {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument,
			"UpdateTable takes an ALTER TABLE statement, got %s", d.Kind)
	}

	if !strings.EqualFold(d.Table, t.Name) {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "ALTER TABLE names table %q, not %q", d.Table, t.Name)
	}

	dropped, err = applyAlterColumns(t, &d.Alter)
	if err != nil {
		return nil, nil, err
	}

	for _, c := range d.Alter.AddColumns {
		added = append(added, c.Name)
	}

	if d.Alter.TTL != nil {
		t.Schema.TTL = *d.Alter.TTL
	}

	t.DDLStatement = ddlFromSchema(t.Name, &t.Schema)

	return added, dropped, nil
}

// applyAlterColumns adds and drops columns, refusing to touch a key column or
// an indexed one, or to add one that is already declared. It returns the
// declared names of the dropped columns.
func applyAlterColumns(t *tableData, spec *AlterSpec) ([]string, error) {
	for _, c := range spec.AddColumns {
		if columnIndex(t, c.Name) >= 0 {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "column %q is already declared on table %q", c.Name, t.Name)
		}

		t.Schema.Columns = append(t.Schema.Columns, c)
	}

	dropped := make([]string, 0, len(spec.DropColumns))

	for _, name := range spec.DropColumns {
		if isKeyColumn(t, name) {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"column %q is part of the primary key of table %q", name, t.Name)
		}

		i := columnIndex(t, name)
		if i < 0 {
			return nil, cerrors.Newf(cerrors.NotFound, "column %q is not declared on table %q", name, t.Name)
		}

		if idx := indexOn(t, name); idx != "" {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"column %q is used by index %q on table %q; drop the index first", name, idx, t.Name)
		}

		dropped = append(dropped, t.Schema.Columns[i].Name)
		t.Schema.Columns = slices.Delete(t.Schema.Columns, i, i+1)
	}

	return dropped, nil
}

// indexOn names an index built on a column, or returns the empty string.
func indexOn(t *tableData, column string) string {
	for _, idx := range t.Indexes {
		for _, k := range idx.Keys {
			if strings.EqualFold(k.ColumnName, column) {
				return idx.Name
			}
		}
	}

	return ""
}

// stripColumns removes dropped columns from every stored row, so a read never
// returns a column the schema no longer declares. Callers must hold m.mu.
func stripColumns(t *tableData, columns []string) {
	if len(columns) == 0 {
		return
	}

	for key, item := range t.items.All() {
		row := maps.Clone(item)
		for _, c := range columns {
			delete(row, c)
		}

		t.items.Set(key, row)
	}
}

// fillColumns sets added columns to their default, or null, on every stored
// row that lacks them, as OCI does for existing rows. Callers must hold m.mu.
func fillColumns(t *tableData, columns []string) {
	if len(columns) == 0 {
		return
	}

	for key, item := range t.items.All() {
		row := maps.Clone(item)

		for _, name := range columns {
			i := columnIndex(t, name)
			if i < 0 {
				continue
			}

			col := &t.Schema.Columns[i]
			if _, ok := row[col.Name]; ok {
				continue
			}

			row[col.Name] = nil

			if col.HasDefault {
				row[col.Name], _ = parseTyped(col, col.DefaultValue)
			}
		}

		t.items.Set(key, row)
	}
}

// columnIndex returns the position of a declared column, matching
// case-insensitively, or -1.
func columnIndex(t *tableData, name string) int {
	return schemaColumn(&t.Schema, name)
}

func isKeyColumn(t *tableData, name string) bool {
	for _, k := range t.Schema.PrimaryKey {
		if strings.EqualFold(k, name) {
			return true
		}
	}

	return false
}

// DeleteOCITable drops a table addressed by name or OCID.
func (m *Mock) DeleteOCITable(_ context.Context, compartmentID, nameOrID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(compartmentID, nameOrID)
	if err != nil {
		return err
	}

	m.dropTable(t)

	return nil
}

// ChangeOCITableCompartment moves a table into another compartment, which
// re-keys it: the destination refuses the move when it already holds a table
// of that name.
func (m *Mock) ChangeOCITableCompartment(_ context.Context, compartmentID, nameOrID, toCompartmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if toCompartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "toCompartmentId is required")
	}

	t, err := m.resolve(compartmentID, nameOrID)
	if err != nil {
		return err
	}

	if toCompartmentID == t.Scope.Compartment {
		return nil
	}

	key := tableKey(toCompartmentID, t.Name)
	if m.tables.Has(key) {
		return cerrors.Newf(cerrors.AlreadyExists,
			"table %q already exists in compartment %q", t.Name, toCompartmentID)
	}

	m.tables.Delete(tableKey(t.Scope.Compartment, t.Name))
	t.Scope = scope.Scope{Compartment: toCompartmentID}
	t.TimeUpdated = m.now()
	m.tables.Set(key, t)
	m.names.Set(t.ID, key)

	return nil
}

func ptr[T any](v T) *T {
	return &v
}
