package cloudids

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	idsdriver "github.com/stackshy/cloudemu/v2/services/cloudids/driver"
)

// maxBodyBytes caps a decoded request body.
const maxBodyBytes = 8 << 20

// outputKeys are the computed / output-only body keys CloudEmu owns. They are
// stripped from an incoming request body so a caller cannot pin them. name,
// createTime, and updateTime are re-injected from the stored endpoint on every
// read (see toResourceJSON). state, endpointForwardingRule, and endpointIp are
// stripped on the way in but NOT re-injected here — they are seeded once at
// create (see seedEndpoint) and thereafter round-trip as stable stored
// passthrough values, so a caller (or a GAPIC client marshaling an output-only
// field) can never override the deterministic value.
//
//nolint:gochecknoglobals // immutable lookup set
var outputKeys = map[string]bool{
	"name":                   true,
	"createTime":             true,
	"updateTime":             true,
	"state":                  true,
	"endpointForwardingRule": true,
	"endpointIp":             true,
}

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true; `response` carries the resulting endpoint (an
// Any for create/patch, absent for delete).
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// decodeBody reads the request body once, normalizes integer enums to their
// canonical names, and returns the caller-supplied fields with the output-only
// keys stripped. The trailing segment of any body `name` is returned so a create
// can fall back to it when the endpointId query param is absent.
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

// toResourceJSON renders a driver endpoint as ids/v1 wire JSON, merging the
// verbatim body fields (including the once-seeded state/endpointForwardingRule/
// endpointIp) with the computed name/createTime/updateTime output fields.
func toResourceJSON(r *idsdriver.Resource) (json.RawMessage, error) {
	m := make(map[string]json.RawMessage, len(r.Fields)+minComputedFields)
	for k, v := range r.Fields {
		m[k] = v
	}

	if err := putJSON(m, "name", resourceName(r.Project, r.Location, r.ID)); err != nil {
		return nil, err
	}

	if err := putJSON(m, "createTime", formatTime(r.CreateTime)); err != nil {
		return nil, err
	}

	if err := putJSON(m, "updateTime", formatTime(r.UpdateTime)); err != nil {
		return nil, err
	}

	return json.Marshal(m)
}

// responseAny wraps an endpoint JSON object as a google.protobuf.Any (adding the
// "@type" discriminator), the shape a completed operation's `response` carries.
func responseAny(resourceJSON json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(resourceJSON, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"` + endpointTypeURL + `"`)

	out, err := json.Marshal(fields)
	if err != nil {
		return nil
	}

	return out
}

// writeEndpoint renders a driver endpoint as ids/v1 wire JSON.
func writeEndpoint(w http.ResponseWriter, res *idsdriver.Resource) {
	raw, err := toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// writeResourceOperation writes a completed operation carrying the endpoint as
// its Any-typed response (create/patch).
func (h *Handler) writeResourceOperation(w http.ResponseWriter, op *idsdriver.Operation, res *idsdriver.Resource) {
	raw, err := toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, responseAny(raw)))
}

// doneOperation builds a completed google.longrunning.Operation and records it
// with the shared LRO poller (a no-op on a nil registry) so a client polling the
// returned name resolves the same done operation (with its response).
func (h *Handler) doneOperation(name string, resp json.RawMessage) operationJSON {
	if h.ops != nil {
		h.ops.Register(name, resp)
	}

	return operationJSON{Name: name, Done: true, Response: resp}
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
