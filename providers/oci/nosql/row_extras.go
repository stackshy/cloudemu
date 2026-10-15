package nosql

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
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
func (m *Mock) GetOCIRow(_ context.Context, compartmentID, nameOrID string, key map[string]string) (*Row, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.resolve(compartmentID, nameOrID)
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
// the row's absence or presence, as OCI's IF_ABSENT and IF_PRESENT do; a write
// the option refuses returns a result with no version rather than an error.
func (m *Mock) PutOCIRow(
	_ context.Context, compartmentID, nameOrID string, value map[string]any, opts WriteOptions,
) (*PutRowResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(compartmentID, nameOrID)
	if err != nil {
		return nil, err
	}

	row, err := prepareRow(t, value, &opts)
	if err != nil {
		return nil, err
	}

	existing, present := t.items.Get(itemKey(t, row))
	present = present && !m.expired(t, existing)

	switch opts.Option {
	case "":
	case OptionIfAbsent:
		if present {
			return &PutRowResult{Existing: toRow(existing)}, nil
		}
	case OptionIfPresent:
		if !present {
			return &PutRowResult{}, nil
		}
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "option %q is not IF_ABSENT or IF_PRESENT", opts.Option)
	}

	return &PutRowResult{Version: toRow(m.putRow(t, row, opts.TTLDays)).Version}, nil
}

// DeleteOCIRow removes one row, reporting whether it was there. OCI's
// DeleteRow answers 200 either way.
func (m *Mock) DeleteOCIRow(_ context.Context, compartmentID, nameOrID string, key map[string]string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.resolve(compartmentID, nameOrID)
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
	row.Version, _ = item[rowVersionColumn].(string)

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

		out[col.Name] = v
	}

	for _, k := range t.Schema.PrimaryKey {
		if _, ok := out[k]; !ok {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "key column %q is missing", k)
		}
	}

	return out, nil
}

// prepareRow checks a write's options and coerces its value.
func prepareRow(t *tableData, value map[string]any, opts *WriteOptions) (map[string]any, error) {
	if opts.TTLDays != nil && *opts.TTLDays < 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "ttl %d is negative", *opts.TTLDays)
	}

	return coerceValue(t, value, opts.ExactMatch)
}

// coerceValue validates a row against the schema: every primary key column
// must be present and non-null, and every value must fit its column's type. A
// column the table does not declare is dropped, or refused when exactMatch is
// set, as OCI's isExactMatch does. A missing nullable column takes its default
// when it has one. Values are stored under the declared column spelling.
func coerceValue(t *tableData, value map[string]any, exactMatch bool) (map[string]any, error) {
	out := make(map[string]any, len(value))

	for name, v := range value {
		i := columnIndex(t, name)
		if i < 0 {
			if exactMatch {
				return nil, cerrors.Newf(cerrors.InvalidArgument, "column %q is not declared on table %q", name, t.Name)
			}

			continue
		}

		if err := setField(t, &t.Schema.Columns[i], v, out); err != nil {
			return nil, err
		}
	}

	for i := range t.Schema.Columns {
		col := &t.Schema.Columns[i]
		if _, ok := out[col.Name]; ok {
			continue
		}

		if err := applyMissingColumn(t, col, out, exactMatch); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// setField fits one value to its column and stores it under the declared
// spelling, refusing a column given twice or a null primary key value.
func setField(t *tableData, col *Column, v any, out map[string]any) error {
	if _, dup := out[col.Name]; dup {
		return cerrors.Newf(cerrors.InvalidArgument, "column %q is given twice", col.Name)
	}

	if v == nil && isKeyColumn(t, col.Name) {
		return cerrors.Newf(cerrors.InvalidArgument, "primary key column %q is null", col.Name)
	}

	typed, err := convertTyped(col, v)
	if err != nil {
		return err
	}

	out[col.Name] = typed

	return nil
}

// applyMissingColumn fills a column the caller left out, or reports why it
// cannot be left out. exactMatch refuses any missing column.
func applyMissingColumn(t *tableData, col *Column, out map[string]any, exactMatch bool) error {
	if isKeyColumn(t, col.Name) {
		return cerrors.Newf(cerrors.InvalidArgument, "primary key column %q is missing from the row", col.Name)
	}

	if exactMatch {
		return cerrors.Newf(cerrors.InvalidArgument, "column %q is missing from the row and isExactMatch is set", col.Name)
	}

	if col.HasDefault {
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

		return checkIntRange(col, n)
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

// convertTyped fits a decoded JSON value to its column's type. A number
// arrives as a json.Number when the body was decoded with UseNumber, which
// keeps a LONG above 2^53 exact, or as a float64 otherwise; an integer column
// refuses a fractional one.
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
		return toFloat(col, v)
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

	// JSON columns hold whatever the caller sent, with numbers as float64.
	return denumber(v), nil
}

func toInteger(col *Column, v any) (any, error) {
	switch n := v.(type) {
	case json.Number:
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err == nil {
			return checkIntRange(col, i)
		}

		// Only an exponent or decimal form like 1e3 or 5.0 may still be integral.
		if errors.Is(err, strconv.ErrRange) || !strings.ContainsAny(n.String(), ".eE") {
			return nil, typeError(col, v)
		}

		f, err := n.Float64()
		if err != nil {
			return nil, typeError(col, v)
		}

		return floatToInteger(col, f)
	case float64:
		return floatToInteger(col, n)
	case int64:
		return checkIntRange(col, n)
	}

	return nil, typeError(col, v)
}

// floatToInteger accepts an integral float inside the int64 range.
func floatToInteger(col *Column, f float64) (any, error) {
	if f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
		return nil, typeError(col, f)
	}

	return checkIntRange(col, int64(f))
}

// checkIntRange refuses an INTEGER value outside the int32 range.
func checkIntRange(col *Column, n int64) (any, error) {
	if col.Type == typeInteger && (n < math.MinInt32 || n > math.MaxInt32) {
		return nil, typeError(col, n)
	}

	return n, nil
}

func toFloat(col *Column, v any) (any, error) {
	switch n := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(n.String(), 64)
		if err != nil {
			return nil, typeError(col, v)
		}

		return f, nil
	case float64:
		return n, nil
	}

	return nil, typeError(col, v)
}

func typeError(col *Column, v any) error {
	return cerrors.Newf(cerrors.InvalidArgument, "value %v does not fit column %q of type %s", v, col.Name, col.Type)
}
