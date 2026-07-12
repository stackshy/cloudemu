package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func writeJSON(w http.ResponseWriter, v any) {
	gcprest.WriteJSON(w, http.StatusOK, v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	gcprest.WriteError(w, status, reason, msg)
}

func writeCErr(w http.ResponseWriter, err error) {
	gcprest.WriteCErr(w, err)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return gcprest.DecodeJSON(w, r, v)
}

// writeOp writes a long-running Operation as the GCP JSON envelope.
func writeOp(w http.ResponseWriter, op *driver.Operation) {
	body := map[string]any{"name": op.Name, "done": op.Done}
	if op.Metadata != nil {
		body["metadata"] = op.Metadata
	}

	if op.Response != nil {
		body["response"] = op.Response
	}

	if op.Error != nil {
		body["error"] = map[string]any{"code": op.Error.Code, "message": op.Error.Message}
	}

	writeJSON(w, body)
}

// typeURLPrefix is the google.protobuf.Any type-URL prefix for aiplatform v1
// messages. SDK pollers read the typed result out of a done operation's
// response, keyed by @type, so done LROs must carry it.
const typeURLPrefix = "type.googleapis.com/google.cloud.aiplatform.v1."

// writeResourceOp writes a done LRO whose response carries the created/affected
// resource (plus its @type) so SDK callers can unmarshal the typed result off
// the operation. A nil resource yields a response with only the @type (e.g.
// UndeployModelResponse). respType is the bare message name, e.g. "Model".
func writeResourceOp(w http.ResponseWriter, op *driver.Operation, resource map[string]any, respType string) {
	body := map[string]any{"name": op.Name, "done": op.Done}
	if op.Metadata != nil {
		body["metadata"] = op.Metadata
	}

	resp := map[string]any{"@type": typeURLPrefix + respType}
	for k, v := range resource {
		resp[k] = v
	}

	body["response"] = resp

	if op.Error != nil {
		body["error"] = map[string]any{"code": op.Error.Code, "message": op.Error.Message}
	}

	writeJSON(w, body)
}

// methodNotAllowed writes a 405 in GCP error shape.
func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
