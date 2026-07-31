package keyspaces

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

func cloneUDT(in *ksdriver.UDT) ksdriver.UDT {
	u := *in
	u.FieldDefinitions = append([]ksdriver.FieldDefinition(nil), u.FieldDefinitions...)
	u.DirectParentTypes = append([]string(nil), u.DirectParentTypes...)
	u.DirectReferringTables = append([]string(nil), u.DirectReferringTables...)

	return u
}

// CreateType creates a user-defined type in an existing keyspace.
func (m *Mock) CreateType(_ context.Context, keyspace, name string, fields []ksdriver.FieldDefinition) (*ksdriver.UDT, error) {
	if err := validName("type", name); err != nil {
		return nil, err
	}

	if len(fields) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "a user-defined type requires at least one field")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.keyspaces.Has(keyspace) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "keyspace %q not found", keyspace)
	}

	key := typeKey(keyspace, name)
	if m.udts.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "type %q already exists in keyspace %q", name, keyspace)
	}

	u := ksdriver.UDT{
		KeyspaceName:     keyspace,
		KeyspaceARN:      m.keyspaceARN(keyspace),
		Name:             name,
		Status:           ksdriver.StatusActive,
		FieldDefinitions: append([]ksdriver.FieldDefinition(nil), fields...),
		MaxNestingDepth:  nestingDepth(fields),
		LastModified:     m.opts.Clock.Now().UTC(),
	}
	m.udts.Set(key, u)

	out := cloneUDT(&u)

	return &out, nil
}

// nestingDepth is 1 plus the number of frozen-collection frozen<> wrappers seen
// in the field types — a simple approximation for the mock.
func nestingDepth(fields []ksdriver.FieldDefinition) int {
	depth := 1

	for _, f := range fields {
		if d := 1 + strings.Count(f.Type, "frozen<"); d > depth {
			depth = d
		}
	}

	return depth
}

// GetType returns a user-defined type.
func (m *Mock) GetType(_ context.Context, keyspace, name string) (*ksdriver.UDT, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.udts.Get(typeKey(keyspace, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "type %q not found in keyspace %q", name, keyspace)
	}

	out := cloneUDT(&u)

	return &out, nil
}

// ListTypes returns the type names of a keyspace, deterministically ordered.
//
//nolint:dupl // the per-keyspace prefix filter mirrors ListTables by design.
func (m *Mock) ListTypes(_ context.Context, keyspace string) ([]ksdriver.UDT, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.keyspaces.Has(keyspace) {
		return nil, cerrors.Newf(cerrors.NotFound, "keyspace %q not found", keyspace)
	}

	prefix := keyspace + "/"
	all := m.udts.SortedValues()
	out := make([]ksdriver.UDT, 0, len(all))

	for i := range all {
		if strings.HasPrefix(typeKey(all[i].KeyspaceName, all[i].Name), prefix) {
			out = append(out, cloneUDT(&all[i]))
		}
	}

	return out, nil
}

// DeleteType removes a user-defined type; it must not be referenced by a table
// or another type.
func (m *Mock) DeleteType(_ context.Context, keyspace, name string) (*ksdriver.UDT, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := typeKey(keyspace, name)

	u, ok := m.udts.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "type %q not found in keyspace %q", name, keyspace)
	}

	if len(u.DirectReferringTables) > 0 || len(u.DirectParentTypes) > 0 {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "type %q is still referenced", name)
	}

	m.udts.Delete(key)

	out := cloneUDT(&u)

	return &out, nil
}
