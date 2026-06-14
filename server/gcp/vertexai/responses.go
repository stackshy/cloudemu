package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/server/wire/gcprest"
	"github.com/stackshy/cloudemu/vertexai/driver"
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

// methodNotAllowed writes a 405 in GCP error shape.
func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
