package cloudtasks

import "encoding/json"

// State enum names (google.cloud.tasks.v2.Queue.State), by ordinal.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var stateNames = []string{"STATE_UNSPECIFIED", "RUNNING", "PAUSED", "DISABLED"}

// httpMethodNames maps the HttpMethod proto integer value to its canonical name
// (google.cloud.tasks.v2.HttpMethod); index 0 (HTTP_METHOD_UNSPECIFIED) maps to
// no name and is never emitted.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var httpMethodNames = []string{"", "POST", "GET", "HEAD", "PUT", "DELETE", "PATCH", "OPTIONS"}

// uriOverrideEnforceModeNames maps the UriOverride.UriOverrideEnforceMode proto
// integer to its canonical name.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var uriOverrideEnforceModeNames = []string{"URI_OVERRIDE_ENFORCE_MODE_UNSPECIFIED", "IF_NOT_EXISTS", "ALWAYS"}

// normalizeEnumNumbers rewrites integer enum values (state, httpMethod,
// uriOverrideEnforceMode) to their canonical names before the body is decoded,
// so a GAPIC client that marshals enums as protojson integers and a string
// client (Terraform's google provider, gcloud) that sends the names both land
// on the same stored value. Only fields whose names uniquely identify a Cloud
// Tasks enum are rewritten, and only when their value is a JSON number, so
// genuine numeric fields are left intact. Because this runs over the whole body
// before decode, enum integers nested inside the opaque httpTarget block are
// normalized too.
func normalizeEnumNumbers(raw []byte) []byte {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw // let the strict decode surface the parse error
	}

	walkEnums(root)

	out, err := json.Marshal(root)
	if err != nil {
		return raw
	}

	return out
}

// walkEnums recursively rewrites integer enum values to their canonical names.
func walkEnums(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if n, ok := val.(float64); ok {
				if name, ok := enumName(k, int(n)); ok {
					t[k] = name
				}

				continue
			}

			walkEnums(val)
		}
	case []any:
		for _, e := range t {
			walkEnums(e)
		}
	}
}

// enumName maps a (field, integer) pair to the canonical enum name, returning
// ok=false when the field is not a known Cloud Tasks enum.
func enumName(field string, n int) (string, bool) {
	switch field {
	case "state":
		return byIndex(stateNames, n)
	case "httpMethod":
		return byIndex(httpMethodNames, n)
	case "uriOverrideEnforceMode":
		return byIndex(uriOverrideEnforceModeNames, n)
	default:
		return "", false
	}
}

// byIndex resolves an enum integer against its ordinal name table, returning
// ok=false for the unspecified/out-of-range default.
func byIndex(names []string, n int) (string, bool) {
	if n <= 0 || n >= len(names) || names[n] == "" {
		return "", false
	}

	return names[n], true
}
