package keyspaces

import (
	"encoding/json"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// writeJSON encodes v as an Amazon Keyspaces response body. The AWS SDK models
// Keyspaces members in lowerCamelCase and its awsjson1.0 deserializer matches
// keys case-sensitively, but encoding/json emits the SDK structs' Go field
// names (UpperCamelCase). This lowercases the first letter of every object key
// so the real client decodes the response. The smithy member name for a
// generated field is its Go name with the leading letter lowered.
func writeJSON(w http.ResponseWriter, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
		return
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
		return
	}

	wire.WriteJSON(w, lowerCamelKeys(decoded))
}

// omitEmptyEnum reports whether an empty value should be dropped because real
// Keyspaces omits the member when it is unset. It covers only
// cdcSpecification.viewType: the SDK models it as a non-pointer string enum, so
// encoding/json renders it as "" on a table without a CDC stream. In the
// Keyspaces SDK, ViewType appears only on CdcSpecification and
// CdcSpecificationSummary, so matching the key name is enough. This is not a
// general rule for empty strings: the decoded map no longer carries Go types,
// so a general rule would also drop non-enum members that are legitimately
// empty. Add another unset enum here by name if one turns up.
func omitEmptyEnum(key string, val any) bool {
	return key == "ViewType" && val == ""
}

// lowerCamelKeys returns v with the first letter of every map key lowercased,
// recursively. It also drops null values, an unset cdcSpecification.viewType
// (see omitEmptyEnum), and the SDK's always-empty resultMetadata envelope so the
// response carries only meaningful fields.
func lowerCamelKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return lowerCamelMap(t)
	case []any:
		for i := range t {
			t[i] = lowerCamelKeys(t[i])
		}

		return t
	case string:
		// encoding/json renders a time.Time (e.g. Table.CreationTimestamp) as an
		// RFC3339 string, but the AWS JSON 1.0 deserializer expects timestamps as
		// epoch-second numbers. Rewrite any value that parses cleanly as RFC3339.
		if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return float64(ts.Unix())
		}

		return v
	default:
		return v
	}
}

func lowerCamelMap(t map[string]any) map[string]any {
	out := make(map[string]any, len(t))

	for k, val := range t {
		if val == nil || k == "ResultMetadata" || omitEmptyEnum(k, val) {
			continue
		}

		out[lowerFirst(k)] = lowerCamelKeys(val)
	}

	return out
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	r, size := utf8.DecodeRuneInString(s)

	return string(unicode.ToLower(r)) + s[size:]
}
