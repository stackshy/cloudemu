package certificatemanager

import "encoding/json"

// managedStateNames maps the ManagedCertificate.State proto integer value to its
// canonical name; index 0 (STATE_UNSPECIFIED) maps to no name and is never
// emitted.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var managedStateNames = []string{"", "PROVISIONING", "FAILED", "ACTIVE"}

// dnsAuthTypeNames maps the DnsAuthorization.Type proto integer value to its
// canonical name; index 0 (TYPE_UNSPECIFIED) maps to no name.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var dnsAuthTypeNames = []string{"", "FIXED_RECORD", "PER_PROJECT_RECORD"}

// scalarEnumFields names the JSON fields whose scalar value is an enum, so a
// numeric value is rewritten to its canonical name. Within Certificate Manager's
// in-scope surface, a numeric `state` is a managed certificate's provisioning
// state and a numeric `type` is a DNS authorization's type.
//
//nolint:gochecknoglobals // immutable lookup set
var scalarEnumFields = map[string][]string{
	"state": managedStateNames,
	"type":  dnsAuthTypeNames,
}

// normalizeEnumNumbers rewrites integer enum values to their canonical names
// before a body is decoded, so a GAPIC client that marshals enums as protojson
// integers and a string client (Terraform's google provider, gcloud) that sends
// the names both land on the same stored value. Only fields whose names uniquely
// identify a Certificate Manager enum are rewritten, and only when their value
// is a JSON number, so genuine numeric fields are left intact. On a parse error
// the body is returned unchanged so the strict decode surfaces it.
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

// rewriteEnumScalar returns the canonical name for a numeric enum value, with
// rewritten=false when val is not a number that resolves to a name (so the
// original value is kept and its children still walked).
func rewriteEnumScalar(val any, names []string) (name string, rewritten bool) {
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
