package kql

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// columnPartLen is the count of colon-separated parts in a "name:type" column
// declaration.
const columnPartLen = 2

// ParseColumnList parses the parenthesised column list of a Kusto table schema
// declaration — the "id:long, name:string, ts:datetime" body of
// .create table T (...) — into an ordered, typed column set. The surrounding
// parentheses may be present or absent. It errors on an empty list, a malformed
// "name:type" pair, a duplicate column name, or an unknown scalar type.
func ParseColumnList(def string) ([]Column, error) {
	def = strings.TrimSpace(def)
	def = strings.TrimPrefix(def, "(")
	def = strings.TrimSuffix(def, ")")
	def = strings.TrimSpace(def)

	if def == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "table schema has no columns")
	}

	cols := make([]Column, 0)
	seen := make(map[string]bool)

	for _, raw := range strings.Split(def, ",") {
		col, err := parseColumn(raw)
		if err != nil {
			return nil, err
		}

		if seen[strings.ToLower(col.Name)] {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "duplicate column: %s", col.Name)
		}

		seen[strings.ToLower(col.Name)] = true

		cols = append(cols, col)
	}

	return cols, nil
}

// parseColumn parses a single "name:type" declaration into a typed Column.
func parseColumn(raw string) (Column, error) {
	parts := strings.SplitN(strings.TrimSpace(raw), ":", columnPartLen)
	if len(parts) != columnPartLen {
		return Column{}, cerrors.Newf(cerrors.InvalidArgument, "malformed column declaration: %q", raw)
	}

	name := strings.TrimSpace(parts[0])
	if name == "" {
		return Column{}, cerrors.Newf(cerrors.InvalidArgument, "empty column name in %q", raw)
	}

	typ, ok := ParseColumnType(parts[1])
	if !ok {
		return Column{}, cerrors.Newf(cerrors.InvalidArgument, "unknown column type: %q", strings.TrimSpace(parts[1]))
	}

	return Column{Name: name, Type: typ}, nil
}
