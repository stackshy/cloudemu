// Package globalaccelerator implements the AWS Global Accelerator control-plane
// API (AWS JSON 1.1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/globalaccelerator client (or the `aws globalaccelerator`
// CLI, or the aws_globalaccelerator_accelerator / _listener / _endpoint_group
// Terraform resources) at a Server registered with this handler and the
// accelerator, listener, endpoint-group, attribute and tagging operations run
// end-to-end against an in-memory driver.
//
// Global Accelerator uses the AWS JSON 1.1 wire shape: POST with a JSON body
// dispatched on the X-Amz-Target header, prefix "GlobalAccelerator_V20180706.".
// It is a GLOBAL service reached in us-west-2; its ARNs carry an empty region
// field. This is a control-plane-only surface: it routes no real traffic and
// runs no health checks.
package globalaccelerator

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	gadriver "github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// TargetPrefix is the X-Amz-Target prefix every Global Accelerator operation
// carries. It is exported so the wire-server registration and the authzgate
// JSON-RPC service map can reference the single source of truth.
const TargetPrefix = "GlobalAccelerator_V20180706."

// Handler serves Global Accelerator JSON-RPC requests against a driver.
type Handler struct {
	ga     gadriver.GlobalAccelerator
	routes map[string]http.HandlerFunc
}

// New returns a Global Accelerator handler backed by d.
func New(d gadriver.GlobalAccelerator) *Handler {
	h := &Handler{ga: d}
	h.routes = map[string]http.HandlerFunc{}
	h.registerAcceleratorRoutes()
	h.registerListenerRoutes()
	h.registerEndpointGroupRoutes()
	h.registerTagRoutes()

	return h
}

// Matches returns true for Global Accelerator requests (X-Amz-Target of
// "GlobalAccelerator_V20180706.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)
}

// ServeHTTP dispatches a Global Accelerator operation based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), TargetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	writeError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Global Accelerator operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error), collapsing the identical
// decode/call/respond boilerplate every operation would otherwise repeat.
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}
