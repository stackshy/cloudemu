package workflows

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	wdriver "github.com/stackshy/cloudemu/v2/services/workflows/driver"
)

// maxBodyBytes caps a decoded request body.
const maxBodyBytes = 8 << 20

// minComputedFields is the extra map capacity reserved for the injected computed
// output fields (name, state, revisionId, createTime, updateTime,
// revisionCreateTime).
const minComputedFields = 6

// outputKeys are the computed / output-only body keys CloudEmu populates itself.
// They are stripped from an incoming request body so a caller cannot pin them,
// and re-injected from the stored resource on every read.
//
//nolint:gochecknoglobals // immutable lookup set
var outputKeys = map[string]bool{
	"name": true, "state": true, "revisionId": true, "createTime": true,
	"updateTime": true, "revisionCreateTime": true, "stateError": true,
	"allKmsKeys": true, "allKmsKeysVersions": true, "cryptoKeyVersion": true,
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

// toResourceJSON renders a driver resource as workflows/v1 wire JSON, merging the
// verbatim body fields with the computed output-only fields.
func toResourceJSON(r *wdriver.Resource) (json.RawMessage, error) {
	// r.Fields is populated from the request body, so bound the map's pre-sized
	// capacity before allocating it. This never drops fields (the map still grows
	// to hold every entry); it only caps the initial allocation hint.
	const maxResourceFields = 10000

	capHint := len(r.Fields) + minComputedFields
	if capHint > maxResourceFields {
		capHint = maxResourceFields
	}

	m := make(map[string]json.RawMessage, capHint)
	for k, v := range r.Fields {
		m[k] = v
	}

	if err := putJSON(m, "name", resourceName(r.Project, r.Location, r.ID)); err != nil {
		return nil, err
	}

	if err := putScalars(m, r); err != nil {
		return nil, err
	}

	return json.Marshal(m)
}

// putScalars adds the computed scalar output fields, stable across reads.
func putScalars(m map[string]json.RawMessage, r *wdriver.Resource) error {
	if err := putJSON(m, "state", r.State); err != nil {
		return err
	}

	if err := putJSON(m, "revisionId", r.RevisionID); err != nil {
		return err
	}

	if err := putJSON(m, "createTime", formatTime(r.CreateTime)); err != nil {
		return err
	}

	if err := putJSON(m, "updateTime", formatTime(r.UpdateTime)); err != nil {
		return err
	}

	return putJSON(m, "revisionCreateTime", formatTime(r.RevisionCreateTime))
}

// responseAny wraps a resource JSON object as a google.protobuf.Any (adding the
// "@type" discriminator), the shape a completed operation's `response` carries.
func responseAny(resourceJSON json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(resourceJSON, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"` + workflowTypeURL + `"`)

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
