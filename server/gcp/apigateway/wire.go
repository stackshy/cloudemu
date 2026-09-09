package apigateway

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	agdriver "github.com/stackshy/cloudemu/v2/services/apigatewaygcp/driver"
)

const (
	// maxBodyBytes caps a decoded request body (openapiDocuments carry base64
	// spec contents, so the ceiling is generous).
	maxBodyBytes = 16 << 20

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, createTime, updateTime).
	minComputedFields = 3
)

// outputKeys are the computed / output-only body keys CloudEmu populates itself.
// They are stripped from an incoming request body so a caller cannot pin them.
// name/createTime/updateTime are re-injected from the stored resource on every
// read; state, serviceConfigId, and defaultHostname are minted once at create
// (see computed.go) and thereafter round-trip as stable stored passthrough
// values, so they are NOT listed here — a create body legitimately never carries
// them, and stripping the seeded copies would drop them from every read.
//
//nolint:gochecknoglobals // immutable lookup set
var outputKeys = map[string]bool{
	"name": true, "createTime": true, "updateTime": true,
}

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true; `response` carries the resulting resource
// (an Any for create/patch, absent for delete).
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// decodeBody reads the request body once and returns the caller-supplied fields
// with the output-only keys stripped. The trailing segment of any body `name` is
// returned so a create can fall back to it when the id query param is absent.
func decodeBody(w http.ResponseWriter, r *http.Request) (fields map[string]json.RawMessage, bodyName string, ok bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return nil, "", false
	}

	all := map[string]json.RawMessage{}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &all); err != nil {
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

// toResourceJSON renders a driver resource as apigateway wire JSON, merging the
// verbatim body fields with the computed name/createTime/updateTime output
// fields. The resource name shape depends on the collection (a config nests
// under its api).
func (c *collection) toResourceJSON(r *agdriver.Resource) (json.RawMessage, error) {
	m := make(map[string]json.RawMessage, len(r.Fields)+minComputedFields)
	for k, v := range r.Fields {
		m[k] = v
	}

	if err := putJSON(m, "name", c.resourceName(r)); err != nil {
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

// resourceName builds the full GCP resource name for a stored resource in this
// collection.
func (c *collection) resourceName(r *agdriver.Resource) string {
	base := "projects/" + r.Project + "/locations/" + r.Location + "/"

	switch c.seg {
	case configsSeg:
		return base + apisSeg + "/" + r.API + "/" + configsSeg + "/" + r.ID
	case gatewaysSeg:
		return base + gatewaysSeg + "/" + r.ID
	default:
		return base + apisSeg + "/" + r.ID
	}
}

// responseAny wraps a resource JSON object as a google.protobuf.Any (adding the
// version-aware "@type" discriminator), the shape a completed operation's
// `response` carries. The proto package matches the request's version so a v1
// SDK and a v1beta Terraform poll each see the type they expect.
func (c *collection) responseAny(version string, resourceJSON json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(resourceJSON, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"type.googleapis.com/google.cloud.apigateway.` + version + "." + c.protoKind + `"`)

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
