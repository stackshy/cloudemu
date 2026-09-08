package datafusion

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

const (
	// maxBodyBytes caps a decoded request body.
	maxBodyBytes = 8 << 20
	// maxProbeBytes caps the body read done during a Matches content probe.
	maxProbeBytes = 1 << 20
)

// outputKeys are the computed / output-only body keys CloudEmu owns. They are
// stripped from an incoming request body so a caller cannot pin them, and are
// re-injected from the stored resource (name, state, createTime, updateTime) or
// derived deterministically (serviceEndpoint, apiEndpoint, gcsBucket,
// tenantProjectId, p4ServiceAccount, serviceAccount, version) on every read.
// version is BOTH an input and an output field: a caller-supplied version is
// carried verbatim in Fields and echoed; only when absent is a deterministic
// default injected — so version is intentionally NOT in this strip set.
//
//nolint:gochecknoglobals // immutable lookup set
var outputKeys = map[string]bool{
	"name": true, "state": true, "stateMessage": true,
	"createTime": true, "updateTime": true,
	"serviceEndpoint": true, "apiEndpoint": true, "gcsBucket": true,
	"tenantProjectId": true, "p4ServiceAccount": true, "serviceAccount": true,
	"serviceAccountEmail": true, "availableVersion": true, "disabledReason": true,
	"maintenanceEvents": true, "workforceIdentityServiceEndpoint": true,
	"satisfiesPzi": true, "satisfiesPzs": true, "enableZoneSeparation": true,
}

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true; `response` carries the resulting instance
// (an Any for create/patch/restart, absent for delete).
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// bodyLooksLikeDataFusion reports whether a POST /instances body is a Data
// Fusion Instance (has a non-empty top-level `type`) rather than a Memorystore
// Redis or Filestore Instance (which carry `tier`/`memorySizeGb`/`fileShares`,
// no top-level `type`). It reads and restores the body so a fall-through still
// sees the full request.
func bodyLooksLikeDataFusion(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxProbeBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		Type json.RawMessage `json:"type"`
	}

	if json.Unmarshal(raw, &probe) != nil {
		return false
	}

	return typeClaimsDataFusion(probe.Type)
}

// typeClaimsDataFusion reports whether a create body's `type` field designates a
// Data Fusion instance. Data Fusion requires a non-UNSPECIFIED instance type, so
// a non-empty string enum (BASIC/ENTERPRISE/DEVELOPER) or a positive
// protojson-integer enum claims the request; TYPE_UNSPECIFIED (0 / "") and an
// absent type do not. Accepting the integer form keeps the dispatch probe
// consistent with normalizeEnumNumbers, which the handler applies to the body.
func typeClaimsDataFusion(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return false
	}

	if s[0] == '"' {
		var str string
		return json.Unmarshal(raw, &str) == nil && str != "" && str != "TYPE_UNSPECIFIED"
	}

	var n int

	return json.Unmarshal(raw, &n) == nil && n > 0
}

// decodeBody reads the request body once, normalizes integer enums to their
// canonical names, and returns the caller-supplied fields with the output-only
// keys stripped. The trailing segment of any body `name` is returned so a create
// can fall back to it when the id query param is absent.
func decodeBody(w http.ResponseWriter, r *http.Request) (fields map[string]json.RawMessage, bodyName string, ok bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return nil, "", false
	}

	all := map[string]json.RawMessage{}

	if len(raw) > 0 {
		if err := json.Unmarshal(normalizeEnumNumbers(raw), &all); err != nil {
			gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
			return nil, "", false
		}
	}

	if n, has := all["name"]; has {
		_ = json.Unmarshal(n, &bodyName)
	}

	out := make(map[string]json.RawMessage, len(all))

	for k, v := range all {
		if outputKeys[k] {
			continue
		}

		out[k] = v
	}

	return out, bodyName, true
}

// toInstanceJSON renders a driver resource as datafusion/v1 wire JSON, merging
// the verbatim body fields with the computed name/state/createTime/updateTime
// output fields and the deterministic create-derived output fields
// (serviceEndpoint, apiEndpoint, gcsBucket, tenantProjectId, p4ServiceAccount,
// serviceAccount, and a default version when none was supplied).
func toInstanceJSON(r *dfdriver.Resource) (json.RawMessage, error) {
	m := make(map[string]json.RawMessage, len(r.Fields)+len(computedOutputs(r)))
	for k, v := range r.Fields {
		m[k] = v
	}

	for k, val := range computedOutputs(r) {
		if err := putJSON(m, k, val); err != nil {
			return nil, err
		}
	}

	// A default version is injected only when the caller supplied none, so a
	// caller-set version round-trips verbatim.
	if _, has := m["version"]; !has {
		if err := putJSON(m, "version", defaultVersion); err != nil {
			return nil, err
		}
	}

	return json.Marshal(m)
}

// responseAny wraps an instance JSON object as a google.protobuf.Any (adding the
// "@type" discriminator), the shape a completed operation's `response` carries.
func responseAny(instanceJSON json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(instanceJSON, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"` + instanceTypeURL + `"`)

	out, err := json.Marshal(fields)
	if err != nil {
		return nil
	}

	return out
}

// putJSON marshals val and stores it under key in m.
func putJSON(m map[string]json.RawMessage, key string, val any) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}

	m[key] = raw

	return nil
}

// formatTime renders t as RFC3339Nano; a zero time renders as the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
