// Package subscriptions implements the Azure Resource Manager subscriptions
// list endpoint.
//
// A caller connecting an Azure account verifies the credential by listing the
// subscriptions it can reach and checking the target is among them. Without
// this endpoint that check fails, so an Azure account cannot be connected at
// all — the failure lands before any resource work begins.
//
// The list is empty: the emulator has no tenant model, so it cannot say which
// subscriptions a credential reaches. Answering with a well-formed empty list
// lets a caller complete the request and decide for itself, rather than
// inventing subscriptions that do not exist.
package subscriptions

import (
	"encoding/json"
	"net/http"
	"strings"
)

const basePath = "/subscriptions"

// Handler serves the subscriptions collection.
type Handler struct{}

// New returns a subscriptions handler.
func New() *Handler {
	return &Handler{}
}

// Matches claims a GET of the subscriptions collection itself. Paths that go
// deeper — /subscriptions/{id}/resourceGroups/... — belong to the resource
// handlers, so only the bare collection is claimed here.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	return strings.TrimSuffix(r.URL.Path, "/") == basePath
}

func (*Handler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	value := []map[string]any{}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
}
