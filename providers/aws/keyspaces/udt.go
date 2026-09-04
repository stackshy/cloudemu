package keyspaces

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// typeMentions reports whether a Cassandra column type string references the
// named UDT as an identifier token (e.g. "address", "frozen<address>",
// "list<frozen<address>>"), without matching substrings of other identifiers.
func typeMentions(colType, name string) bool {
	for _, tok := range strings.FieldsFunc(colType, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if tok == name {
			return true
		}
	}

	return false
}

// registerTableUDTRefs records table→UDT references so DeleteType's in-use guard
// is live: for every UDT in the keyspace mentioned by a column type, the table
// is added to that UDT's DirectReferringTables. The caller holds the lock.
func (m *Mock) registerTableUDTRefs(keyspace, table string, schema *ksdriver.SchemaDefinition) {
	prefix := keyspace + "/"

	for _, key := range m.udts.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		u, ok := m.udts.Get(key)
		if !ok {
			continue
		}

		if schemaMentionsType(schema, u.Name) && !containsStr(u.DirectReferringTables, table) {
			u.DirectReferringTables = append(u.DirectReferringTables, table)
			m.udts.Set(key, u)
		}
	}
}

// unregisterTableUDTRefs drops a table from every UDT's referring-tables list in
// the keyspace. The caller holds the lock.
func (m *Mock) unregisterTableUDTRefs(keyspace, table string) {
	prefix := keyspace + "/"

	for _, key := range m.udts.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		u, ok := m.udts.Get(key)
		if !ok {
			continue
		}

		if containsStr(u.DirectReferringTables, table) {
			u.DirectReferringTables = removeStr(u.DirectReferringTables, table)
			m.udts.Set(key, u)
		}
	}
}

func schemaMentionsType(s *ksdriver.SchemaDefinition, name string) bool {
	for _, c := range s.AllColumns {
		if typeMentions(c.Type, name) {
			return true
		}
	}

	return false
}

func removeStr(items []string, s string) []string {
	out := items[:0:0]

	for _, v := range items {
		if v != s {
			out = append(out, v)
		}
	}

	return out
}

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

	if !m.keyspaces.Has(keyspace) {
		return nil, cerrors.Newf(cerrors.NotFound, "keyspace %q not found", keyspace)
	}

	u, ok := m.udts.Get(typeKey(keyspace, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "type %q not found in keyspace %q", name, keyspace)
	}

	out := cloneUDT(&u)

	return &out, nil
}

// ListTypes returns the type names of a keyspace, deterministically ordered.
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

	if !m.keyspaces.Has(keyspace) {
		return nil, cerrors.Newf(cerrors.NotFound, "keyspace %q not found", keyspace)
	}

	key := typeKey(keyspace, name)

	u, ok := m.udts.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "type %q not found in keyspace %q", name, keyspace)
	}

	if len(u.DirectReferringTables) > 0 || len(u.DirectParentTypes) > 0 {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "type %q is still referenced by %v", name, u.DirectReferringTables)
	}

	m.udts.Delete(key)

	out := cloneUDT(&u)

	return &out, nil
}
