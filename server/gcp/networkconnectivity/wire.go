package networkconnectivity

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	nccdriver "github.com/stackshy/cloudemu/v2/services/networkconnectivity/driver"
)

// jsonRaw aliases json.RawMessage so the handler's collection type can name the
// passthrough body-field map without importing encoding/json directly.
type jsonRaw = json.RawMessage

// maxBodyBytes caps a decoded request body.
const maxBodyBytes = 8 << 20

// spokeLinkFields are the mutually-exclusive linked_* oneof keys a spoke body
// carries. Exactly one must be present at create.
//
//nolint:gochecknoglobals // immutable lookup set
var spokeLinkFields = []string{
	"linkedVpcNetwork",
	"linkedVpnTunnels",
	"linkedInterconnectAttachments",
	"linkedRouterApplianceInstances",
}

// outputKeys are the computed / output-only body keys CloudEmu populates itself.
// They are stripped from an incoming request body so a caller cannot pin them,
// and re-injected from the stored resource on every read.
//
//nolint:gochecknoglobals // immutable lookup set
var outputKeys = map[string]bool{
	"name": true, "uniqueId": true, "state": true, "createTime": true, "updateTime": true,
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

// validateSpokeLink enforces the spoke linked_* oneof: exactly one of the four
// mutually-exclusive link blocks must be present. Zero or multiple is a 400, as
// real Network Connectivity Center rejects it.
func validateSpokeLink(fields map[string]jsonRaw) error {
	count := 0

	for _, k := range spokeLinkFields {
		if _, ok := fields[k]; ok {
			count++
		}
	}

	if count != 1 {
		return cerrors.New(cerrors.InvalidArgument,
			"a spoke must set exactly one of linkedVpcNetwork, linkedVpnTunnels, "+
				"linkedInterconnectAttachments, linkedRouterApplianceInstances")
	}

	return nil
}

// toResourceJSON renders a driver resource as networkconnectivity/v1 wire JSON,
// merging the verbatim body fields with the computed name/uniqueId/state/
// createTime/updateTime output fields.
func (c *collection) toResourceJSON(r *nccdriver.Resource) (json.RawMessage, error) {
	m := make(map[string]json.RawMessage, len(r.Fields)+minComputedFields)
	for k, v := range r.Fields {
		m[k] = v
	}

	pairs := []struct {
		key string
		val any
	}{
		{"name", resourceName(c.seg, r.Project, r.Location, r.ID)},
		{"uniqueId", r.UniqueID},
		{"state", r.State},
		{"createTime", formatTime(r.CreateTime)},
		{"updateTime", formatTime(r.UpdateTime)},
	}

	for _, p := range pairs {
		if err := putJSON(m, p.key, p.val); err != nil {
			return nil, err
		}
	}

	return json.Marshal(m)
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
