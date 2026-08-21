package nosql

import (
	"context"
	"maps"
	"sort"

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

	limits, err := normaliseLimits(spec.Limits)
	if err != nil {
		return nil, err
	}

	if existing, ok := m.tables.Get(d.Table); ok {
		if d.IfNotExists {
			return ptr(toTable(existing)), nil
		}

		return nil, cerrors.Newf(cerrors.AlreadyExists, "table %q already exists", d.Table)
	}

	t, err := m.newTable(d.Table, normaliseStatement(spec.DDLStatement), &d.Schema, limits)
	if err != nil {
		return nil, err
	}

	t.Scope = scope.Scope{Compartment: spec.CompartmentID}
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

		return l, nil
	}

	return l, cerrors.Newf(cerrors.InvalidArgument, "capacityMode %q is not ON_DEMAND or PROVISIONED", l.CapacityMode)
}

// GetOCITable returns a table by name or OCID.
func (m *Mock) GetOCITable(_ context.Context, nameOrID string) (*Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(nameOrID)
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
	names := m.tables.Keys()
	sort.Strings(names)

	out := make([]Table, 0, len(names))

	for _, n := range names {
		t, ok := m.tables.Get(n)
		if !ok || !t.Scope.Matches(filter) {
			continue
		}

		if name != "" && t.Name != name {
			continue
		}

		out = append(out, toTable(t))
	}

	return out, nil
}

// UpdateOCITable applies an ALTER TABLE statement, new limits, tags and the
// auto-reclaim flag. Every field is optional, as UpdateTable's are.
func (m *Mock) UpdateOCITable(_ context.Context, nameOrID string, upd TableUpdate) (*Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return nil, err
	}

	if upd.DDLStatement != "" {
		if err := applyAlter(t, upd.DDLStatement); err != nil {
			return nil, err
		}
	}

	if upd.Limits != nil {
		limits, err := normaliseLimits(*upd.Limits)
		if err != nil {
			return nil, err
		}

		t.Limits = limits
	}

	if upd.IsAutoReclaimable != nil {
		t.IsAutoReclaimable = *upd.IsAutoReclaimable
	}

	if upd.FreeformTags != nil {
		t.Tags = maps.Clone(upd.FreeformTags)
	}

	t.TimeUpdated = m.now()

	return ptr(toTable(t)), nil
}

// applyAlter applies one ALTER TABLE statement to a table. Callers must hold m.mu.
func applyAlter(t *tableData, statement string) error {
	d, err := ParseDDL(statement)
	if err != nil {
		return err
	}

	if d.Kind != DDLAlterTable {
		return cerrors.Newf(cerrors.InvalidArgument, "UpdateTable takes an ALTER TABLE statement, got %s", d.Kind)
	}

	if d.Table != t.Name {
		return cerrors.Newf(cerrors.InvalidArgument, "ALTER TABLE names table %q, not %q", d.Table, t.Name)
	}

	if err := applyAlterColumns(t, &d.Alter); err != nil {
		return err
	}

	if d.Alter.TTL != nil {
		t.Schema.TTL = *d.Alter.TTL
	}

	t.DDLStatement = ddlFromSchema(t.Name, &t.Schema)

	return nil
}

// applyAlterColumns adds and drops columns, refusing to touch a key column or
// to add one that is already declared.
func applyAlterColumns(t *tableData, spec *AlterSpec) error {
	for _, c := range spec.AddColumns {
		if columnIndex(t, c.Name) >= 0 {
			return cerrors.Newf(cerrors.AlreadyExists, "column %q is already declared on table %q", c.Name, t.Name)
		}

		t.Schema.Columns = append(t.Schema.Columns, c)
	}

	for _, name := range spec.DropColumns {
		if isKeyColumn(t, name) {
			return cerrors.Newf(cerrors.InvalidArgument, "column %q is part of the primary key of table %q", name, t.Name)
		}

		i := columnIndex(t, name)
		if i < 0 {
			return cerrors.Newf(cerrors.NotFound, "column %q is not declared on table %q", name, t.Name)
		}

		t.Schema.Columns = append(t.Schema.Columns[:i], t.Schema.Columns[i+1:]...)
	}

	return nil
}

func columnIndex(t *tableData, name string) int {
	for i, c := range t.Schema.Columns {
		if c.Name == name {
			return i
		}
	}

	return -1
}

func isKeyColumn(t *tableData, name string) bool {
	for _, k := range t.Schema.PrimaryKey {
		if k == name {
			return true
		}
	}

	return false
}

// DeleteOCITable drops a table addressed by name or OCID.
func (m *Mock) DeleteOCITable(_ context.Context, nameOrID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return err
	}

	return m.dropTable(t.Name)
}

// ChangeOCITableCompartment moves a table into another compartment.
func (m *Mock) ChangeOCITableCompartment(_ context.Context, nameOrID, compartmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if compartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	t, err := m.resolve(nameOrID)
	if err != nil {
		return err
	}

	t.Scope = scope.Scope{Compartment: compartmentID}
	t.TimeUpdated = m.now()

	return nil
}

func ptr[T any](v T) *T {
	return &v
}
