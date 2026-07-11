// Package loadbalancer implements the GCP Cloud Load Balancing REST API
// (backendServices + forwardingRules) against a CloudEmu loadbalancer driver.
// Real cloud.google.com/go/compute/apiv1 BackendServices and
// GlobalForwardingRules clients configured with a custom endpoint hit this
// handler the same way they hit compute.googleapis.com.
//
// Registration / shadowing: this handler shares the /compute/v1/projects/…
// URL space with the existing compute (server/gcp/compute) and networks
// (server/gcp/networks) handlers, but claims a disjoint set of resource types —
// backendServices / forwardingRules — whereas compute claims instances /
// operations / disks / snapshots / images and networks claims networks /
// subnetworks / firewalls. Because gcprest.ParsePath keys dispatch on the
// resource-type segment, first-match-wins routing is unambiguous and the three
// handlers can register in any order. Folding into the existing handlers was
// unnecessary since there is no route overlap. NOTE: mutating operations return
// compute#operation envelopes the SDK polls at
// /compute/v1/projects/{p}/global/operations/{name}, which the compute handler
// serves — so wire the Compute handler alongside this one when the SDK's
// Insert/Delete pollers are exercised.
//
// Driver-abstraction mapping (GCP → loadbalancer driver):
//
//	global/backendServices/{name}     → TargetGroup  (Insert/Get/List/Delete)
//	global/forwardingRules/{name}     → LoadBalancer (Insert/Get/List/Delete);
//	                                    a forwarding rule that references a
//	                                    backendService also creates a Listener
//	                                    linking the load balancer to that target
//	                                    group.
//
// Both GCP and the driver key resources by their user-assigned name (the driver
// preserves Name verbatim), so the handler resolves SDK-facing names to driver
// records via a Describe scan.
package loadbalancer

import (
	"net/http"

	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

const (
	resourceBackendServices = "backendServices"
	resourceForwardingRules = "forwardingRules"
)

// Handler serves the GCP load-balancing REST surface.
type Handler struct {
	lb lbdriver.LoadBalancer
}

// New returns a GCP load balancer handler backed by lb.
func New(lb lbdriver.LoadBalancer) *Handler {
	return &Handler{lb: lb}
}

// Matches returns true for /compute/v1/.../backendServices|forwardingRules
// URLs. Disjoint from the compute (instances/operations/disks/…) and networks
// (networks/subnetworks/firewalls) handlers, so registration order is
// unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rp.ResourceType {
	case resourceBackendServices, resourceForwardingRules:
		return true
	}

	return false
}

// ServeHTTP routes the request based on resource type and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed path")
		return
	}

	switch rp.ResourceType {
	case resourceBackendServices:
		h.routeBackendServices(w, r, rp)
	case resourceForwardingRules:
		h.routeForwardingRules(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown resource type")
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeBackendServices(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertBackendService(w, r, rp)
		case http.MethodGet:
			h.listBackendServices(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBackendService(w, r, rp)
	case http.MethodDelete:
		h.deleteBackendService(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeForwardingRules(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertForwardingRule(w, r, rp)
		case http.MethodGet:
			h.listForwardingRules(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getForwardingRule(w, r, rp)
	case http.MethodDelete:
		h.deleteForwardingRule(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}
