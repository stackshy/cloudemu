package dataplex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

// stateActive is the state every Dataplex resource is minted with. Real Dataplex
// transitions CREATING → ACTIVE; CloudEmu has no provisioning data plane and
// completes synchronously, so it reports a stable ACTIVE that a Terraform refresh
// reconciles cleanly (state is output-only/computed in both the API and the
// provider schema).
const stateActive = "ACTIVE"

// statusReady, statusNone, statusScheduled, and statusDisabled are the stable
// nested-status states CloudEmu reports for a synchronously-provisioned resource.
const (
	statusReady     = "READY"
	statusNone      = "NONE"
	statusScheduled = "SCHEDULED"
	statusDisabled  = "DISABLED"
)

// injectLakeComputed writes a lake's output-only fields: a deterministic uid, the
// ACTIVE state, the service account, and the aggregated asset/metastore status
// blocks. All are derived from stable inputs so every read is byte-identical.
func injectLakeComputed(_ *route, r *dpdriver.Resource, m map[string]json.RawMessage) {
	injectIdentity(r, m)
	_ = putJSON(m, "serviceAccount", serviceAccount(r.Project))
	_ = putJSON(m, "assetStatus", assetStatusBlock(r))
	_ = putJSON(m, "metastoreStatus", map[string]any{
		"state":      statusNone,
		"updateTime": formatTime(r.CreateTime),
	})
}

// injectZoneComputed writes a zone's output-only fields: a deterministic uid, the
// ACTIVE state, and the aggregated asset status block.
func injectZoneComputed(_ *route, r *dpdriver.Resource, m map[string]json.RawMessage) {
	injectIdentity(r, m)
	_ = putJSON(m, "assetStatus", assetStatusBlock(r))
}

// injectAssetComputed writes an asset's output-only fields: a deterministic uid,
// the ACTIVE state, and the resource/security/discovery status blocks. The
// discovery status tracks whether the asset (or its spec) enables discovery.
func injectAssetComputed(_ *route, r *dpdriver.Resource, m map[string]json.RawMessage) {
	injectIdentity(r, m)

	ts := formatTime(r.CreateTime)
	_ = putJSON(m, "resourceStatus", map[string]any{"state": statusReady, "updateTime": ts})
	_ = putJSON(m, "securityStatus", map[string]any{"state": statusReady, "updateTime": ts})
	_ = putJSON(m, "discoveryStatus", map[string]any{"state": discoveryState(r), "updateTime": ts})
}

// injectIdentity writes the uid and state fields shared by every resource level.
func injectIdentity(r *dpdriver.Resource, m map[string]json.RawMessage) {
	_ = putJSON(m, "uid", deterministicUID(resourceName(r)))
	_ = putJSON(m, "state", stateActive)
}

// assetStatusBlock is the aggregated status a lake or zone reports over its
// underlying assets. CloudEmu does not run the asset roll-up, so it reports a
// stable zero-count block stamped with the resource's create time.
func assetStatusBlock(r *dpdriver.Resource) map[string]any {
	return map[string]any{
		"activeAssets":                 0,
		"securityPolicyApplyingAssets": 0,
		"updateTime":                   formatTime(r.CreateTime),
	}
}

// serviceAccount is the Dataplex service agent a lake runs as, in the real API's
// per-project form. The value is output-only/computed, so a stable value derived
// from the project never drifts a refresh.
func serviceAccount(project string) string {
	return "service-" + project + "@gcp-sa-dataplex.iam.gserviceaccount.com"
}

// discoveryState reports whether an asset's discovery is scheduled or disabled,
// read from its discoverySpec.enabled (an asset with no spec inherits its zone's
// discovery, reported here as disabled for a stable minimal value).
func discoveryState(r *dpdriver.Resource) string {
	spec, ok := objectField(r.Fields, "discoverySpec")
	if !ok {
		return statusDisabled
	}

	var enabled bool
	if raw, has := spec["enabled"]; has {
		_ = json.Unmarshal(raw, &enabled)
	}

	if enabled {
		return statusScheduled
	}

	return statusDisabled
}

// deterministicUID derives a stable, uuid-shaped identifier from a resource's
// full name, so a resource's uid is fixed the moment it exists and never changes
// across reads (the classic computed-field drift point).
func deterministicUID(name string) string {
	sum := sha256.Sum256([]byte(name))
	h := hex.EncodeToString(sum[:])

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// objectField decodes a top-level body field as a JSON object, reporting
// ok=false when it is absent, null, or not an object.
func objectField(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, bool) {
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}

	return obj, true
}
