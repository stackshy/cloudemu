package bigtable

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func cloneGCRule(in *btdriver.GCRule) *btdriver.GCRule {
	if in == nil {
		return nil
	}

	out := *in

	if in.Union != nil {
		out.Union = make([]btdriver.GCRule, len(in.Union))
		for i := range in.Union {
			out.Union[i] = *cloneGCRule(&in.Union[i])
		}
	}

	if in.Intersection != nil {
		out.Intersection = make([]btdriver.GCRule, len(in.Intersection))
		for i := range in.Intersection {
			out.Intersection[i] = *cloneGCRule(&in.Intersection[i])
		}
	}

	return &out
}

func cloneColumnFamilies(src map[string]btdriver.ColumnFamily) map[string]btdriver.ColumnFamily {
	if src == nil {
		return nil
	}

	out := make(map[string]btdriver.ColumnFamily, len(src))
	for k, v := range src {
		out[k] = btdriver.ColumnFamily{GCRule: cloneGCRule(v.GCRule)}
	}

	return out
}

func cloneTable(in *btdriver.Table) btdriver.Table {
	t := *in
	t.ColumnFamilies = cloneColumnFamilies(in.ColumnFamilies)

	return t
}

// CreateTable creates a table in an instance.
func (m *Mock) CreateTable(_ context.Context, cfg btdriver.CreateTableConfig) (*btdriver.Table, error) {
	if cfg.TableID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "tableId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(cfg.Parent) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "instance %q not found", cfg.Parent)
	}

	name := cfg.Parent + "/tables/" + cfg.TableID
	if m.tables.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "table %q already exists", name)
	}

	t := btdriver.Table{
		Name:               name,
		ColumnFamilies:     cloneColumnFamilies(cfg.ColumnFamilies),
		Granularity:        orDefault(cfg.Granularity, "MILLIS"),
		DeletionProtection: cfg.DeletionProtection,
	}
	m.tables.Set(name, t)

	out := cloneTable(&t)

	return &out, nil
}

// GetTable returns a table by full name.
func (m *Mock) GetTable(_ context.Context, name string) (*btdriver.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.getVisibleTable(name)
}

// getVisibleTable returns a non-deleted table. The caller holds a lock.
func (m *Mock) getVisibleTable(name string) (*btdriver.Table, error) {
	t, ok := m.tables.Get(name)
	if !ok || t.Deleted {
		return nil, cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	out := cloneTable(&t)

	return &out, nil
}

// ListTables returns the (non-deleted) tables of an instance.
func (m *Mock) ListTables(_ context.Context, instance string) ([]btdriver.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := instance + "/tables/"
	all := m.tables.SortedValues()
	out := make([]btdriver.Table, 0, len(all))

	for i := range all {
		if strings.HasPrefix(all[i].Name, prefix) && !all[i].Deleted {
			out = append(out, cloneTable(&all[i]))
		}
	}

	return out, nil
}

// UpdateTable patches a table's deletion-protection flag (LRO).
func (m *Mock) UpdateTable(_ context.Context, name string, deletionProtection *bool) (*btdriver.Table, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tables.Get(name)
	if !ok || t.Deleted {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	if deletionProtection != nil {
		t.DeletionProtection = *deletionProtection
	}

	m.tables.Set(name, t)

	op := m.newOp("update-table", name)
	out := cloneTable(&t)

	return &out, op, nil
}

// DeleteTable soft-deletes a table (undeletable for a window in real Bigtable).
func (m *Mock) DeleteTable(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tables.Get(name)
	if !ok || t.Deleted {
		return cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	if t.DeletionProtection {
		return cerrors.Newf(cerrors.FailedPrecondition, "table %q has deletion protection enabled", name)
	}

	t.Deleted = true
	m.tables.Set(name, t)

	return nil
}

// UndeleteTable restores a soft-deleted table (LRO).
func (m *Mock) UndeleteTable(_ context.Context, name string) (*btdriver.Table, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tables.Get(name)
	if !ok || !t.Deleted {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "no deleted table %q to undelete", name)
	}

	t.Deleted = false
	m.tables.Set(name, t)

	op := m.newOp("undelete-table", name)
	out := cloneTable(&t)

	return &out, op, nil
}

// ModifyColumnFamilies applies create/update/drop modifications atomically.
func (m *Mock) ModifyColumnFamilies(
	_ context.Context, name string, mods []btdriver.ColumnFamilyModification,
) (*btdriver.Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tables.Get(name)
	if !ok || t.Deleted {
		return nil, cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	families := cloneColumnFamilies(t.ColumnFamilies)
	if families == nil {
		families = map[string]btdriver.ColumnFamily{}
	}

	for i := range mods {
		if err := applyColumnFamilyMod(families, &mods[i]); err != nil {
			return nil, err
		}
	}

	t.ColumnFamilies = families
	m.tables.Set(name, t)

	out := cloneTable(&t)

	return &out, nil
}

func applyColumnFamilyMod(families map[string]btdriver.ColumnFamily, mod *btdriver.ColumnFamilyModification) error {
	switch {
	case mod.Create != nil:
		if _, exists := families[mod.ID]; exists {
			return cerrors.Newf(cerrors.AlreadyExists, "column family %q already exists", mod.ID)
		}

		families[mod.ID] = btdriver.ColumnFamily{GCRule: cloneGCRule(mod.Create.GCRule)}
	case mod.Update != nil:
		if _, exists := families[mod.ID]; !exists {
			return cerrors.Newf(cerrors.NotFound, "column family %q not found", mod.ID)
		}

		families[mod.ID] = btdriver.ColumnFamily{GCRule: cloneGCRule(mod.Update.GCRule)}
	case mod.Drop:
		if _, exists := families[mod.ID]; !exists {
			return cerrors.Newf(cerrors.NotFound, "column family %q not found", mod.ID)
		}

		delete(families, mod.ID)
	}

	return nil
}

// DropRowRange is a data-plane operation; the mock validates the table exists
// and reports success without storing row data.
func (m *Mock) DropRowRange(_ context.Context, name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if t, ok := m.tables.Get(name); !ok || t.Deleted {
		return cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	return nil
}

// GenerateConsistencyToken issues an opaque token for the table.
func (m *Mock) GenerateConsistencyToken(_ context.Context, name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if t, ok := m.tables.Get(name); !ok || t.Deleted {
		return "", cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	return fmt.Sprintf("token-%s-%d", lastSegment(name), m.opSeq.Add(1)), nil
}

// CheckConsistency reports the token as consistent (no replication lag in the
// mock).
func (m *Mock) CheckConsistency(_ context.Context, name, token string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if t, ok := m.tables.Get(name); !ok || t.Deleted {
		return false, cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	return token != "", nil
}

// RestoreTable creates a new table from a backup (LRO).
func (m *Mock) RestoreTable(_ context.Context, parent, tableID, backup string) (*btdriver.Table, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(parent) {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "instance %q not found", parent)
	}

	b, ok := m.backups.Get(backup)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "backup %q not found", backup)
	}

	name := parent + "/tables/" + tableID
	if m.tables.Has(name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "table %q already exists", name)
	}

	t := btdriver.Table{
		Name: name, Granularity: "MILLIS", SourceBackup: b.Name,
		ColumnFamilies: cloneColumnFamilies(b.ColumnFamilies),
	}
	m.tables.Set(name, t)

	op := m.newOp("restore-table", name)
	out := cloneTable(&t)

	return &out, op, nil
}
