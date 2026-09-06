package clouddeploy

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cdriver "github.com/stackshy/cloudemu/v2/services/clouddeploy/driver"
)

// maxBodyBytes caps a decoded request body.
const maxBodyBytes = 8 << 20

// outputKeys are the computed / output-only body keys CloudEmu populates itself.
// They are stripped from an incoming request body so a caller cannot pin them,
// and re-injected from the stored resource on every read.
//
//nolint:gochecknoglobals // immutable lookup set
var outputKeys = map[string]bool{
	"name": true, "uid": true, "pipelineUid": true, "createTime": true,
	"updateTime": true, "etag": true, "condition": true, "targetId": true,
}

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true; `response` carries the resulting resource
// (an Any for create/patch, absent for delete).
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
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

// toResourceJSON renders a driver resource as clouddeploy/v1 wire JSON, merging
// the verbatim body fields with the computed output-only fields. col carries the
// per-collection injection (condition for a pipeline, targetId for a target).
func (c *collection) toResourceJSON(r *cdriver.Resource) (json.RawMessage, error) {
	m := make(map[string]json.RawMessage, len(r.Fields)+minComputedFields)
	for k, v := range r.Fields {
		m[k] = v
	}

	if err := putJSON(m, "name", resourceName(c.seg, r.Project, r.Location, r.ID)); err != nil {
		return nil, err
	}

	if err := putScalars(m, r); err != nil {
		return nil, err
	}

	if err := c.injectComputed(m, r); err != nil {
		return nil, err
	}

	return json.Marshal(m)
}

// putScalars adds the shared computed scalar output fields.
func putScalars(m map[string]json.RawMessage, r *cdriver.Resource) error {
	if err := putJSON(m, "uid", r.UID); err != nil {
		return err
	}

	if err := putJSON(m, "createTime", formatTime(r.CreateTime)); err != nil {
		return err
	}

	if err := putJSON(m, "updateTime", formatTime(r.UpdateTime)); err != nil {
		return err
	}

	return putJSON(m, "etag", r.Etag)
}

// injectComputed adds the per-collection computed output field: a pipeline's
// condition (derived from updateTime, stable across reads) or a target's
// targetId (a copy of the id).
func (c *collection) injectComputed(m map[string]json.RawMessage, r *cdriver.Resource) error {
	if c.seg == pipelinesColl {
		return putJSON(m, "condition", pipelineCondition(r.UpdateTime))
	}

	return putJSON(m, "targetId", r.ID)
}

// pipelineCondition builds the computed PipelineCondition a freshly created or
// updated delivery pipeline reports. It is derived from the stable updateTime so
// a Terraform refresh reads the same value every time. The provider ignores the
// value (output-only in the TF schema); the requirement is an identity
// round-trip.
func pipelineCondition(updateTime time.Time) map[string]any {
	stamp := formatTime(updateTime)

	return map[string]any{
		"pipelineReadyCondition":  map[string]any{"status": true, "updateTime": stamp},
		"targetsPresentCondition": map[string]any{"status": true, "updateTime": stamp},
	}
}

// responseAny wraps a resource JSON object as a google.protobuf.Any (adding the
// "@type" discriminator), the shape a completed operation's `response` carries.
func (c *collection) responseAny(resourceJSON json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(resourceJSON, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"` + c.typeURL + `"`)

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
