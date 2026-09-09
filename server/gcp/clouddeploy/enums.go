package clouddeploy

import "encoding/json"

// usageNames maps the ExecutionEnvironmentUsage proto integer value to its
// canonical name; index 0 (…_UNSPECIFIED) maps to no name and is never emitted.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var usageNames = []string{"", "RENDER", "DEPLOY", "VERIFY", "PREDEPLOY", "POSTDEPLOY"}

// arrayEnumFields names the JSON fields whose value is an array of enums, so a
// numeric element is rewritten to its canonical name.
//
//nolint:gochecknoglobals // immutable lookup set
var arrayEnumFields = map[string][]string{
	"usages": usageNames,
}

// normalizeEnumNumbers rewrites integer enum values to their canonical names
// before a body is decoded, so a GAPIC client that marshals enums as protojson
// integers and a string client (Terraform's google provider, gcloud) that sends
// the names both land on the same stored value. Only fields whose names uniquely
// identify a Cloud Deploy enum are rewritten, and only when their value is a JSON
// number, so genuine numeric fields are left intact. On a parse error the body
// is returned unchanged so the strict decode surfaces it.
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

// walkEnums recursively rewrites integer enum values to their canonical names.
func walkEnums(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if names, ok := arrayEnumFields[k]; ok {
				if arr, isArr := val.([]any); isArr {
					rewriteEnumArray(arr, names)
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

// rewriteEnumArray replaces each numeric element of an enum array with its
// canonical name, in place.
func rewriteEnumArray(arr []any, names []string) {
	for i, e := range arr {
		n, ok := e.(float64)
		if !ok {
			continue
		}

		if name, ok := byIndex(names, int(n)); ok {
			arr[i] = name
		}
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
