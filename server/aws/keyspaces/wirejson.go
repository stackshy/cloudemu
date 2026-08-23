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
// so the real client decodes the response — the smithy member name for a
// generated field is exactly its Go name with the leading letter lowered.
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

// lowerCamelKeys returns v with the first letter of every map key lowercased,
// recursively. It also drops null values and the SDK's always-empty
// resultMetadata envelope so the response carries only meaningful fields.
func lowerCamelKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, val := range t {
			if val == nil || k == "ResultMetadata" {
				continue
			}

			out[lowerFirst(k)] = lowerCamelKeys(val)
		}

		return out
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

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	r, size := utf8.DecodeRuneInString(s)

	return string(unicode.ToLower(r)) + s[size:]
}
