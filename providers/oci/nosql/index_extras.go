package nosql

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// CreateOCIIndex builds a secondary index from OCI's key list.
func (m *Mock) CreateOCIIndex(_ context.Context, nameOrID string, spec IndexSpec, ifNotExists bool) (*Index, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return nil, err
	}

	if existing, findErr := findIndex(t, spec.Name); findErr == nil {
		if ifNotExists {
			return cloneIndex(existing), nil
		}

		return nil, cerrors.Newf(cerrors.AlreadyExists, "index %q already exists on table %q", spec.Name, t.Name)
	}

	idx, err := m.addIndex(t, spec)
	if err != nil {
		return nil, err
	}

	return cloneIndex(idx), nil
}

// GetOCIIndex returns one index on a table.
func (m *Mock) GetOCIIndex(_ context.Context, nameOrID, indexName string) (*Index, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return nil, err
	}

	idx, err := findIndex(t, indexName)
	if err != nil {
		return nil, err
	}

	return cloneIndex(idx), nil
}

// ListOCIIndexes returns a table's indexes ordered by name. A non-empty
// indexName narrows the listing, as OCI's name query parameter does.
func (m *Mock) ListOCIIndexes(_ context.Context, nameOrID, indexName string) ([]Index, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return nil, err
	}

	out := make([]Index, 0, len(t.Indexes))

	for _, idx := range t.Indexes {
		if indexName != "" && idx.Name != indexName {
			continue
		}

		out = append(out, *cloneIndex(idx))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// DeleteOCIIndex drops an index. isIfExists makes dropping a missing index a
// no-op, as OCI's query parameter of that name does.
func (m *Mock) DeleteOCIIndex(_ context.Context, nameOrID, indexName string, ifExists bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return err
	}

	if err := m.dropIndex(t, indexName); err != nil {
		if ifExists && cerrors.GetCode(err) == cerrors.NotFound {
			return nil
		}

		return err
	}

	return nil
}

// OCITableScope returns the compartment a table lives in, which the handler
// stamps on the work requests it records.
func (m *Mock) OCITableScope(nameOrID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return ""
	}

	return t.Scope.Compartment
}

func cloneIndex(idx *Index) *Index {
	out := *idx
	out.Keys = append([]IndexKey(nil), idx.Keys...)

	return &out
}
