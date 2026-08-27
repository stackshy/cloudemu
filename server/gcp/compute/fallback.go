package compute

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Fallback claims any /compute/v1/ request that no real compute-space handler
// (instances, networks, load balancing, …) matched, and answers with a proper
// GCP JSON error envelope instead of the dispatcher's bare-text 501 with the
// wrong Content-Type. Register it LAST among the /compute/v1 handlers so
// first-match-wins keeps every implemented path on its real handler.
type Fallback struct{}

// NewFallback returns the compute-space catch-all handler.
func NewFallback() *Fallback { return &Fallback{} }

// Matches reports whether the request targets the compute API URL space.
func (*Fallback) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, gcprest.BasePrefix)
}

// ServeHTTP writes a GCP-style JSON 501 for an unimplemented compute path.
func (*Fallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented",
		"not implemented: "+r.Method+" "+r.URL.Path)
}
