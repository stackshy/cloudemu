package metastore

import "encoding/json"

// Ordinal enum name tables for the Dataproc Metastore surface. Index 0 is the
// *_UNSPECIFIED default and maps to no name (never emitted). The ordinals match
// the metastore.v1 proto so a GAPIC client that marshals an enum as a protojson
// integer lands on the same stored value a string client (Terraform, gcloud)
// sends.
//
//nolint:gochecknoglobals // immutable ordinal enum tables
var (
	tierNames           = []string{"", "DEVELOPER", "ENTERPRISE"}
	databaseTypeNames   = []string{"", "MYSQL", "SPANNER"}
	releaseChannelNames = []string{"", "CANARY", "STABLE"}
	stateNames          = []string{
		"", "CREATING", "ACTIVE", "SUSPENDING", "SUSPENDED",
		"UPDATING", "DELETING", "ERROR", "AUTOSCALING", "MIGRATING", "PROXY",
	}
	logFormatNames        = []string{"", "LEGACY", "JSON"}
	endpointProtocolNames = []string{"", "THRIFT", "GRPC"}
)

// scalarEnumFields names the JSON fields whose scalar value is an enum, so a
// numeric value is rewritten to its canonical name. The names are unique within
// the metastore surface (tier, databaseType, releaseChannel, and the output-only
// state on the service; logFormat and endpointProtocol on the nested telemetry /
// Hive configs), so genuine numeric fields (port, hourOfDay, scalingFactor) are
// never touched.
//
//nolint:gochecknoglobals // immutable lookup set
var scalarEnumFields = map[string][]string{
	"tier":             tierNames,
	"databaseType":     databaseTypeNames,
	"releaseChannel":   releaseChannelNames,
	"state":            stateNames,
	"logFormat":        logFormatNames,
	"endpointProtocol": endpointProtocolNames,
}

// normalizeEnumNumbers rewrites integer enum values to their canonical names
// before a body is decoded, so a GAPIC client that marshals enums as protojson
// integers and a string client (Terraform's google provider, gcloud) that sends
// the names both land on the same stored value. Only fields whose names uniquely
// identify a metastore enum are rewritten, and only when their value is a JSON
// number. On a parse error the body is returned unchanged so the strict decode
// surfaces it.
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
