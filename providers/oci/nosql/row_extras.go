package nosql

import (
	"context"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Write options UpdateRow accepts.
const (
	OptionIfAbsent  = "IF_ABSENT"
	OptionIfPresent = "IF_PRESENT"
)

// GetOCIRow returns one row by primary key. The key arrives as the wire's
// column:value strings and is coerced to the column types the schema declares.
func (m *Mock) GetOCIRow(_ context.Context, nameOrID string, key map[string]string) (*Row, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return nil, err
	}

	k, err := coerceKey(t, key)
	if err != nil {
		return nil, err
	}

	item, ok := t.items.Get(itemKey(t, k))
	if !ok || m.expired(t, item) {
		return nil, cerrors.New(cerrors.NotFound, "row not found")
	}

	return toRow(item), nil
}

// PutOCIRow writes a row. The option, when set, makes the write conditional on
// the row's absence or presence, as OCI's IF_ABSENT and IF_PRESENT do.
func (m *Mock) PutOCIRow(_ context.Context, nameOrID string, value map[string]any, option string) (*Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return nil, err
	}

	row, err := coerceValue(t, value)
	if err != nil {
		return nil, err
	}

	existing, present := t.items.Get(itemKey(t, row))
	present = present && !m.expired(t, existing)

	switch option {
	case "":
	case OptionIfAbsent:
		if present {
			return nil, cerrors.New(cerrors.FailedPrecondition, "row already exists and option is IF_ABSENT")
		}
	case OptionIfPresent:
		if !present {
			return nil, cerrors.New(cerrors.FailedPrecondition, "row does not exist and option is IF_PRESENT")
		}
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "option %q is not IF_ABSENT or IF_PRESENT", option)
	}

	return toRow(m.putRow(t, row)), nil
}

// DeleteOCIRow removes one row, reporting whether it was there. OCI's
// DeleteRow answers 200 either way.
func (m *Mock) DeleteOCIRow(_ context.Context, nameOrID string, key map[string]string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(nameOrID)
	if err != nil {
		return false, err
	}

	k, err := coerceKey(t, key)
	if err != nil {
		return false, err
	}

	stored := itemKey(t, k)

	item, ok := t.items.Get(stored)
	if ok && m.expired(t, item) {
		ok = false
	}

	t.items.Delete(stored)

	return ok, nil
}

// toRow projects a stored row onto the OCI shape, moving the internal expiry
// out of the value and into the metadata OCI reports it as.
func toRow(item map[string]any) *Row {
	row := &Row{Value: visible(item)}

	if exp, ok := toUnix(item[ttlExpiryColumn]); ok && exp > 0 {
		row.TimeOfExpiration = time.Unix(exp, 0).UTC().Format(timeFormat)
	}

	return row
}

// coerceKey turns the wire's column:value strings into a primary key,
// requiring every key column and refusing anything that is not one.
func coerceKey(t *tableData, key map[string]string) (map[string]any, error) {
	if len(key) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "key is required")
	}

	out := make(map[string]any, len(key))

	for name, raw := range key {
		if !isKeyColumn(t, name) {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "%q is not a primary key column of table %q", name, t.Name)
		}

		col := t.Schema.Columns[columnIndex(t, name)]

		v, err := parseTyped(&col, raw)
		if err != nil {
			return nil, err
		}

		out[name] = v
	}

	for _, k := range t.Schema.PrimaryKey {
		if _, ok := out[k]; !ok {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "key column %q is missing", k)
		}
	}

	return out, nil
}

// coerceValue validates a row against the schema: every column must be
// declared, every primary key column present, and every value must fit its
// column's type. A missing nullable column takes its default when it has one.
func coerceValue(t *tableData, value map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(value))

	for name, v := range value {
		i := columnIndex(t, name)
		if i < 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "column %q is not declared on table %q", name, t.Name)
		}

		typed, err := convertTyped(&t.Schema.Columns[i], v)
		if err != nil {
			return nil, err
		}

		out[name] = typed
	}

	for i := range t.Schema.Columns {
		col := &t.Schema.Columns[i]
		if _, ok := out[col.Name]; ok {
			continue
		}

		if err := applyMissingColumn(t, col, out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// applyMissingColumn fills a column the caller left out, or reports why it
// cannot be left out.
func applyMissingColumn(t *tableData, col *Column, out map[string]any) error {
	if isKeyColumn(t, col.Name) {
		return cerrors.Newf(cerrors.InvalidArgument, "primary key column %q is missing from the row", col.Name)
	}

	if col.DefaultValue != "" {
		v, err := parseTyped(col, col.DefaultValue)
		if err != nil {
			return err
		}

		out[col.Name] = v

		return nil
	}

	if !col.IsNullable {
		return cerrors.Newf(cerrors.InvalidArgument, "column %q is NOT NULL and has no default", col.Name)
	}

	return nil
}

// parseTyped reads a column value from its string form, which is how key
// values and DDL defaults arrive.
func parseTyped(col *Column, raw string) (any, error) {
	switch col.Type {
	case typeInteger, typeLong:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, typeError(col, raw)
		}

		return n, nil
	case typeFloat, typeDouble, typeNumber:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, typeError(col, raw)
		}

		return f, nil
	case typeBoolean:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, typeError(col, raw)
		}

		return b, nil
	}

	return raw, nil
}

// convertTyped fits a decoded JSON value to its column's type. JSON has one
// number type, so an integer column takes a whole float64 and refuses a
// fractional one.
func convertTyped(col *Column, v any) (any, error) {
	if v == nil {
		if !col.IsNullable {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "column %q is NOT NULL", col.Name)
		}

		return nil, nil //nolint:nilnil // a null column value is the value
	}

	switch col.Type {
	case typeInteger, typeLong:
		return toInteger(col, v)
	case typeFloat, typeDouble, typeNumber:
		f, ok := v.(float64)
		if !ok {
			return nil, typeError(col, v)
		}

		return f, nil
	case typeBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, typeError(col, v)
		}

		return b, nil
	case typeString, typeBinary, typeTimestamp:
		s, ok := v.(string)
		if !ok {
			return nil, typeError(col, v)
		}

		return s, nil
	}

	// JSON columns hold whatever the caller sent.
	return v, nil
}

func toInteger(col *Column, v any) (any, error) {
	f, ok := v.(float64)
	if !ok {
		return nil, typeError(col, v)
	}

	if f != float64(int64(f)) {
		return nil, typeError(col, v)
	}

	return int64(f), nil
}

func typeError(col *Column, v any) error {
	return cerrors.Newf(cerrors.InvalidArgument, "value %v does not fit column %q of type %s", v, col.Name, col.Type)
}
