package apigateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// activeState is the state every API Gateway resource is minted with. Real API
// Gateway transitions CREATING → ACTIVE as it provisions; CloudEmu has no
// provisioning data plane, so it reports a stable ACTIVE. state is output-only
// in both the API and the Terraform schema, so a stable value never drifts a
// refresh, and seeding ACTIVE lets a Terraform apply reconcile clean.
const activeState = "ACTIVE"

// seedAPI injects the output-only state so a GET reports it stably.
func seedAPI(fields map[string]json.RawMessage, _ *route) {
	putString(fields, "state", activeState)
}

// seedConfig injects the output-only state and a deterministic serviceConfigId,
// minted once from the config's full name so a GET reports it stably across
// refreshes. serviceConfigId is a classic drift point: it is server-assigned at
// create and must be identical on the create response and every later read.
func seedConfig(fields map[string]json.RawMessage, rt *route) {
	putString(fields, "state", activeState)

	if !hasValue(fields, "serviceConfigId") {
		full := "projects/" + rt.project + "/locations/" + rt.location + "/apis/" + rt.api + "/configs/" + rt.name
		putString(fields, "serviceConfigId", rt.name+"-"+shortHash(full))
	}
}

// seedGateway injects the output-only state and a deterministic defaultHostname,
// minted once from the gateway's full name so a GET reports it stably. The shape
// mirrors real API Gateway's `<gateway_id>-<hash>.<region>.gateway.dev`; the
// value is output-only, so its stability (not its exact real-world form) is what
// keeps a Terraform refresh drift-free.
func seedGateway(fields map[string]json.RawMessage, rt *route) {
	putString(fields, "state", activeState)

	if !hasValue(fields, "defaultHostname") {
		full := "projects/" + rt.project + "/locations/" + rt.location + "/gateways/" + rt.name
		host := rt.name + "-" + shortHash(full) + "." + rt.location + ".gateway.dev"
		putString(fields, "defaultHostname", host)
	}
}

// shortHash returns a stable 8-hex-char digest of s, used to derive the
// deterministic serviceConfigId / defaultHostname discriminators.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])[:8]
}

// putString sets key to the JSON string val, overwriting only an absent or empty
// value so a caller-supplied value (never sent for these output-only keys, but
// defensive) is left intact.
func putString(fields map[string]json.RawMessage, key, val string) {
	if hasValue(fields, key) {
		return
	}

	fields[key] = json.RawMessage(`"` + val + `"`)
}

// hasValue reports whether key is present in fields with a non-null, non-empty
// value.
func hasValue(fields map[string]json.RawMessage, key string) bool {
	v, ok := fields[key]

	return ok && len(v) > 0 && string(v) != "null" && string(v) != `""`
}
