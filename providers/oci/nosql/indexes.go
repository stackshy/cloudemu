package nosql

import (
	"context"
	"maps"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// CreateIndex builds a secondary index on a table.
func (m *Mock) CreateIndex(_ context.Context, table string, cfg driver.GSIConfig) (*driver.IndexInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return nil, err
	}

	idx, err := m.addIndex(t, indexFromGSI(&cfg))
	if err != nil {
		return nil, err
	}

	return toIndexInfo(idx), nil
}

// addIndex records an index on a table. Callers must hold m.mu.
func (m *Mock) addIndex(t *tableData, spec IndexSpec) (*Index, error) {
	if spec.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "index name is required")
	}

	if len(spec.Columns) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "an index names at least one column")
	}

	for _, idx := range t.Indexes {
		if idx.Name == spec.Name {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "index %q already exists on table %q", spec.Name, t.Name)
		}
	}

	declared := map[string]bool{}
	for _, c := range t.Schema.Columns {
		declared[c.Name] = true
	}

	idx := &Index{Name: spec.Name, LifecycleState: StateActive}

	for _, c := range spec.Columns {
		if !declared[c] {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "index column %q is not declared on table %q", c, t.Name)
		}

		idx.Keys = append(idx.Keys, IndexKey{ColumnName: c})
	}

	t.Indexes = append(t.Indexes, idx)
	t.TimeUpdated = m.now()

	return idx, nil
}

// DeleteIndex drops a secondary index.
func (m *Mock) DeleteIndex(_ context.Context, table, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return err
	}

	return m.dropIndex(t, indexName)
}

// dropIndex removes an index from a table. Callers must hold m.mu.
func (m *Mock) dropIndex(t *tableData, name string) error {
	for i, idx := range t.Indexes {
		if idx.Name != name {
			continue
		}

		t.Indexes = append(t.Indexes[:i], t.Indexes[i+1:]...)
		t.TimeUpdated = m.now()

		return nil
	}

	return cerrors.Newf(cerrors.NotFound, "index %q not found on table %q", name, t.Name)
}

// DescribeIndex returns one secondary index.
func (m *Mock) DescribeIndex(_ context.Context, table, indexName string) (*driver.IndexInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.lookup(table)
	if err != nil {
		return nil, err
	}

	idx, err := findIndex(t, indexName)
	if err != nil {
		return nil, err
	}

	return toIndexInfo(idx), nil
}

// ListIndexes returns every secondary index on a table.
func (m *Mock) ListIndexes(_ context.Context, table string) ([]driver.IndexInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.lookup(table)
	if err != nil {
		return nil, err
	}

	out := make([]driver.IndexInfo, 0, len(t.Indexes))
	for _, idx := range t.Indexes {
		out = append(out, *toIndexInfo(idx))
	}

	return out, nil
}

// findIndex returns a named index. Callers must hold m.mu.
func findIndex(t *tableData, name string) (*Index, error) {
	for _, idx := range t.Indexes {
		if idx.Name == name {
			return idx, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "index %q not found on table %q", name, t.Name)
}

func toIndexInfo(idx *Index) *driver.IndexInfo {
	cfg := toGSIConfig(idx)

	return &driver.IndexInfo{
		Name:         cfg.Name,
		PartitionKey: cfg.PartitionKey,
		SortKey:      cfg.SortKey,
		Status:       idx.LifecycleState,
	}
}

// UpdateTTL configures the attribute-based TTL. It sits alongside the
// table-level TTL the DDL sets rather than replacing it: a row expires when
// either says so.
func (m *Mock) UpdateTTL(_ context.Context, table string, cfg driver.TTLConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return err
	}

	if cfg.Enabled && cfg.AttributeName == "" {
		return cerrors.New(cerrors.InvalidArgument, "an enabled TTL names an attribute")
	}

	if cfg.AttributeName == ttlExpiryColumn {
		return cerrors.Newf(cerrors.InvalidArgument, "%q is reserved for the table-level TTL", ttlExpiryColumn)
	}

	t.ttl = cfg

	return nil
}

// DescribeTTL returns the attribute-based TTL configuration.
func (m *Mock) DescribeTTL(_ context.Context, table string) (*driver.TTLConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.lookup(table)
	if err != nil {
		return nil, err
	}

	cfg := t.ttl

	return &cfg, nil
}

// UpdateStreamConfig reports that OCI has no change stream. NoSQL Database
// publishes no DynamoDB-Streams or Cosmos-change-feed equivalent, so there is
// nothing to enable rather than a feature left unimplemented.
func (m *Mock) UpdateStreamConfig(_ context.Context, table string, _ driver.StreamConfig) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.lookup(table); err != nil {
		return err
	}

	return cerrors.New(cerrors.Unimplemented, "OCI NoSQL Database publishes no change stream")
}

// GetStreamRecords reports that OCI has no change stream. See UpdateStreamConfig.
func (m *Mock) GetStreamRecords(
	_ context.Context, table string, _ int, _ string,
) (*driver.StreamIterator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.lookup(table); err != nil {
		return nil, err
	}

	return nil, cerrors.New(cerrors.Unimplemented, "OCI NoSQL Database publishes no change stream")
}

// TagResource merges freeform tags onto a table.
func (m *Mock) TagResource(_ context.Context, table string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return err
	}

	if t.Tags == nil {
		t.Tags = make(map[string]string, len(tags))
	}

	maps.Copy(t.Tags, tags)
	t.TimeUpdated = m.now()

	return nil
}

// UntagResource removes freeform tag keys from a table.
func (m *Mock) UntagResource(_ context.Context, table string, tagKeys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return err
	}

	for _, k := range tagKeys {
		delete(t.Tags, k)
	}

	t.TimeUpdated = m.now()

	return nil
}

// ListTagsOfResource returns a table's freeform tags.
func (m *Mock) ListTagsOfResource(_ context.Context, table string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.lookup(table)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(t.Tags))
	maps.Copy(out, t.Tags)

	return out, nil
}
