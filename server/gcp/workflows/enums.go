package workflows

import "encoding/json"

// scalarEnumFields maps a JSON field whose value is a single enum to its ordinal
// name table; index 0 (…_UNSPECIFIED) maps to no name and is never rewritten.
// This lets a GAPIC client that marshals enums as protojson integers and a
// string client (Terraform's google provider, gcloud) that sends the names both
// land on the same stored value. Only fields whose names uniquely identify a
// Cloud Workflows enum are rewritten, and only when their value is a JSON number.
//
//nolint:gochecknoglobals // immutable ordinal enum tables
var scalarEnumFields = map[string][]string{
	"callLogLevel":          {"", "LOG_ALL_CALLS", "LOG_ERRORS_ONLY", "LOG_NONE"},
	"executionHistoryLevel": {"", "EXECUTION_HISTORY_BASIC", "EXECUTION_HISTORY_DETAILED"},
}

// normalizeEnumNumbers rewrites integer enum values to their canonical names
// before a body is decoded. On a parse error the body is returned unchanged so
// the strict decode surfaces it.
func normalizeEnumNumbers(raw []byte) []byte {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw
	}

	walkEnums(root)

	out, err := json.Marshal(root)
	if err != nil {
		return raw
	}

	return out
}

// walkEnums recursively rewrites integer scalar enum values to their canonical
// names.
func walkEnums(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if names, ok := scalarEnumFields[k]; ok {
				if name, rewritten := rewriteEnumScalar(val, names); rewritten {
					t[k] = name
					continue
				}
			}

			walkEnums(val)
		}
	case []any:
		for _, e := range t {
			walkEnums(e)
		}
	}
}

// rewriteEnumScalar resolves a numeric enum value against its ordinal name
// table, reporting whether a rewrite applied. A non-numeric value (already a
// string name) is left for the caller to recurse into.
func rewriteEnumScalar(val any, names []string) (string, bool) {
	n, ok := val.(float64)
	if !ok {
		return "", false
	}

	return byIndex(names, int(n))
}

// byIndex resolves an enum integer against its ordinal name table, returning
// ok=false for the unspecified/out-of-range default.
func byIndex(names []string, n int) (string, bool) {
	if n <= 0 || n >= len(names) || names[n] == "" {
		return "", false
	}

	return names[n], true
}
