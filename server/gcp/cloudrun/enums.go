package cloudrun

import "encoding/json"

// The Cloud Run v2 GAPIC REST client (cloud.google.com/go/run/apiv2) marshals
// request bodies with protojson's UseEnumNumbers option, so every enum field —
// ingress, launchStage, executionEnvironment, vpcAccess.egress, traffic.type,
// condition.state — arrives on the wire as its integer value rather than its
// canonical name. Real Cloud Run's protojson-based server accepts both forms;
// string clients (Terraform's google provider, gcloud) send the names. To match
// the server for every client, normalizeEnumNumbers rewrites any integer enum
// value back to its canonical name before the body is decoded, so the driver
// stores and echoes the same names regardless of which client wrote them.
//
// Only fields whose names uniquely identify a Cloud Run enum are rewritten, and
// only when their value is a JSON number, so genuine numeric fields (ports,
// task counts, traffic percent, instance counts, generations) are left intact.
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
// ok=false when the field is not a known Cloud Run enum (so the value is left
// untouched). Each table is indexed by the enum's proto integer value — index 0
// is the UNSPECIFIED default and is never emitted, so it maps to no name — and
// mirrors google.cloud.run.v2 / google.api.LaunchStage exactly.
func enumName(field string, n int) (string, bool) {
	switch field {
	case "ingress":
		return byIndex(ingressNames(), n)
	case "launchStage":
		return byIndex(launchStageNames(), n)
	case "executionEnvironment":
		return byIndex(execEnvNames(), n)
	case "egress":
		return byIndex(egressNames(), n)
	case "type":
		return byIndex(trafficTypeNames(), n)
	case "state":
		return byIndex(conditionStateNames(), n)
	default:
		return "", false
	}
}

// byIndex resolves an enum's integer value against its ordinal name table,
// returning ok=false for the unspecified/out-of-range default.
func byIndex(names []string, n int) (string, bool) {
	if n <= 0 || n >= len(names) || names[n] == "" {
		return "", false
	}

	return names[n], true
}

func ingressNames() []string {
	return []string{
		"", // INGRESS_TRAFFIC_UNSPECIFIED
		"INGRESS_TRAFFIC_ALL",
		"INGRESS_TRAFFIC_INTERNAL_ONLY",
		"INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER",
		"INGRESS_TRAFFIC_NONE",
	}
}

func launchStageNames() []string {
	return []string{
		"", // LAUNCH_STAGE_UNSPECIFIED
		"EARLY_ACCESS",
		"ALPHA",
		"BETA",
		"GA",
		"DEPRECATED",
		"UNIMPLEMENTED",
		"PRELAUNCH",
	}
}

func execEnvNames() []string {
	return []string{
		"", // EXECUTION_ENVIRONMENT_UNSPECIFIED
		"EXECUTION_ENVIRONMENT_GEN1",
		"EXECUTION_ENVIRONMENT_GEN2",
	}
}

func egressNames() []string {
	return []string{
		"", // VPC_EGRESS_UNSPECIFIED
		"ALL_TRAFFIC",
		"PRIVATE_RANGES_ONLY",
	}
}

func trafficTypeNames() []string {
	return []string{
		"", // TRAFFIC_TARGET_ALLOCATION_TYPE_UNSPECIFIED
		"TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST",
		"TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION",
	}
}

func conditionStateNames() []string {
	return []string{
		"", // STATE_UNSPECIFIED
		"CONDITION_PENDING",
		"CONDITION_RECONCILING",
		"CONDITION_FAILED",
		"CONDITION_SUCCEEDED",
	}
}
