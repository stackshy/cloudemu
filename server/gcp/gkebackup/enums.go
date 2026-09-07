package gkebackup

import "encoding/json"

// Restore-config policy enum ordinal tables. Index 0 is the *_UNSPECIFIED
// default and maps to no name (never emitted). The orderings match the
// gkebackup v1 proto so a protojson client that marshals an enum as its integer
// value lands on the same canonical name a string client (Terraform's google
// provider, gcloud) sends.
//
//nolint:gochecknoglobals // immutable ordinal enum tables
var (
	clusterResourceConflictPolicyNames = []string{"", "USE_EXISTING_VERSION", "USE_BACKUP_VERSION"}
	namespacedResourceRestoreModeNames = []string{
		"", "DELETE_AND_RESTORE", "FAIL_ON_CONFLICT",
		"MERGE_SKIP_ON_CONFLICT", "MERGE_REPLACE_VOLUME_ON_CONFLICT", "MERGE_REPLACE_ON_CONFLICT",
	}
	volumeDataRestorePolicyNames = []string{
		"", "RESTORE_VOLUME_DATA_FROM_BACKUP", "REUSE_VOLUME_HANDLE_FROM_BACKUP", "NO_VOLUME_DATA_RESTORATION",
	}
)

// scalarEnumFields names the JSON fields (anywhere in the body tree) whose
// scalar value is a Backup for GKE enum, so a numeric value is rewritten to its
// canonical name. These live deep inside a restorePlan's restoreConfig raw
// passthrough block; the field names are unique to their enum.
//
//nolint:gochecknoglobals // immutable lookup set
var scalarEnumFields = map[string][]string{
	"clusterResourceConflictPolicy": clusterResourceConflictPolicyNames,
	"namespacedResourceRestoreMode": namespacedResourceRestoreModeNames,
	"volumeDataRestorePolicy":       volumeDataRestorePolicyNames,
}

// normalizeEnumNumbers rewrites integer enum values to their canonical names
// before a body is decoded, so a GAPIC client that marshals enums as protojson
// integers and a string client (Terraform's google provider, gcloud) both land
// on the same stored value. Only fields whose names uniquely identify a Backup
// for GKE enum are rewritten, and only when their value is a JSON number, so
// genuine numeric fields are left intact. On a parse error the body is returned
// unchanged so the strict decode surfaces it.
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
