package binaryauthorization

import "encoding/json"

// globalPolicyEvaluationModeNames maps the
// Policy.GlobalPolicyEvaluationMode proto integer to its canonical name; index 0
// (unspecified) maps to no name and is never rewritten.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var globalPolicyEvaluationModeNames = []string{"GLOBAL_POLICY_EVALUATION_MODE_UNSPECIFIED", "ENABLE", "DISABLE"}

// evaluationModeNames maps the AdmissionRule.EvaluationMode proto integer to its
// canonical name.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var evaluationModeNames = []string{"EVALUATION_MODE_UNSPECIFIED", "ALWAYS_ALLOW", "REQUIRE_ATTESTATION", "ALWAYS_DENY"}

// enforcementModeNames maps the AdmissionRule.EnforcementMode proto integer to
// its canonical name.
//
//nolint:gochecknoglobals // immutable ordinal enum table
var enforcementModeNames = []string{"ENFORCEMENT_MODE_UNSPECIFIED", "ENFORCED_BLOCK_AND_AUDIT_LOG", "DRYRUN_AUDIT_LOG_ONLY"}

// normalizeEnumNumbers rewrites integer enum values (globalPolicyEvaluationMode,
// evaluationMode, enforcementMode) to their canonical names before the body is
// decoded, so a GAPIC client that marshals enums as protojson integers and a
// string client (Terraform's google provider, gcloud) that sends the names both
// land on the same stored value. Only fields whose names uniquely identify a
// Binary Authorization enum are rewritten, and only when their value is a JSON
// number, so genuine numeric fields are left intact. Because this runs over the
// whole body before decode, enum integers nested inside the opaque
// defaultAdmissionRule / clusterAdmissionRules blocks are normalized too.
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
// ok=false when the field is not a known Binary Authorization enum.
func enumName(field string, n int) (string, bool) {
	switch field {
	case "globalPolicyEvaluationMode":
		return byIndex(globalPolicyEvaluationModeNames, n)
	case "evaluationMode":
		return byIndex(evaluationModeNames, n)
	case "enforcementMode":
		return byIndex(enforcementModeNames, n)
	default:
		return "", false
	}
}

// byIndex resolves an enum integer against its ordinal name table, returning
// ok=false for the unspecified/out-of-range default.
func byIndex(names []string, n int) (string, bool) {
	if n <= 0 || n >= len(names) {
		return "", false
	}

	return names[n], true
}
