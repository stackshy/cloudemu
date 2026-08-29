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
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

const (
	resourceBackendServices = "backendServices"
	resourceForwardingRules = "forwardingRules"
)

// actionGetHealth is the POST action on a named backend service that returns the
// health of its backends (compute.backendServices.getHealth).
const actionGetHealth = "getHealth"

// reasonResourceInUse is the GCP error reason returned when a delete is rejected
// because another resource still references the target. Real GCP answers such a
// delete with HTTP 400 and this reason, so dependents are never orphaned.
const reasonResourceInUse = "resourceInUseByAnotherResource"

// Handler serves the GCP load-balancing REST surface.
type Handler struct {
	lb lbdriver.LoadBalancer
	// ops records the compute#operation names this handler mints so the compute
	// handler's shared /operations route (which serves LB operation polls)
	// resolves a real operation and 404s a bogus one. Nil in a package-level
	// server (every operation poll answered DONE, legacy behavior).
	ops *gcprest.OperationRegistry
}

// New returns a GCP load balancer handler backed by lb.
func New(lb lbdriver.LoadBalancer) *Handler {
	return &Handler{lb: lb}
}

// SetOperationRegistry wires the shared compute-operation registry so the
// operations this handler mints are resolvable (and unknown names 404) through
// the compute handler's /operations poll route.
func (h *Handler) SetOperationRegistry(reg *gcprest.OperationRegistry) { h.ops = reg }

// Matches returns true for the load-balancing resource types — backendServices,
// forwardingRules, healthChecks, targetPools, urlMaps, the L7 front-end chain
// (targetHttpProxies, targetHttpsProxies, sslCertificates) and instanceGroups /
// regionInstanceGroups. Disjoint from the compute (instances/operations/disks/…)
// and networks (networks/subnetworks/firewalls) handlers, so registration order
// is unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := parseLBPath(r.URL.Path)
	if !ok {
		return false
	}

	// Aggregated-scope requests (aggregatedList) are not implemented for these
	// load-balancer resources; leave them unmatched so the dispatcher's default
	// applies rather than mis-serving them as a scoped list.
	if rp.Scope == gcprest.ScopeAggregated {
		return false
	}

	switch rp.ResourceType {
	case resourceBackendServices, resourceForwardingRules,
		resourceHealthChecks, resourceTargetPools, resourceURLMaps,
		resourceTargetHTTPProxies, resourceTargetHTTPSProxies, resourceSslCertificates,
		resourceInstanceGroups, resourceRegionInstanceGroups:
		return true
	}

	return false
}

// ServeHTTP routes the request based on resource type and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := parseLBPath(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed path")
		return
	}

	switch rp.ResourceType {
	case resourceBackendServices:
		h.routeBackendServices(w, r, rp)
	case resourceForwardingRules:
		h.routeForwardingRules(w, r, rp)
	case resourceHealthChecks, resourceTargetPools, resourceURLMaps:
		h.routeGCPResource(w, r, rp)
	case resourceTargetHTTPProxies, resourceTargetHTTPSProxies, resourceSslCertificates,
		resourceInstanceGroups, resourceRegionInstanceGroups:
		h.routeL7Resource(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown resource type")
	}
}

//nolint:gocritic // rp is a request-scoped value
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

	// getHealth is a POST action on a named backend service, distinct from the
	// resource-level verbs below.
	if r.Method == http.MethodPost && rp.Action == actionGetHealth {
		h.getBackendServiceHealth(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBackendService(w, r, rp)
	case http.MethodPatch, http.MethodPut:
		h.patchBackendService(w, r, rp)
	case http.MethodDelete:
		h.deleteBackendService(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
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

// parseLBPath parses a load-balancing request path, adding one form the generic
// gcprest.ParsePath deliberately rejects: the scopeless-global action URL the
// compute SDK uses for a few global target-proxy actions, e.g.
// /compute/v1/projects/{p}/targetHttpProxies/{name}/setUrlMap (no /global/
// segment). Only the two global target-proxy types opt into that form; every
// other path still goes through the strict parser unchanged.
func parseLBPath(path string) (gcprest.ResourcePath, bool) {
	if rp, ok := gcprest.ParsePath(path); ok {
		return rp, true
	}

	const projects = gcprest.BasePrefix + "projects/"
	if !strings.HasPrefix(path, projects) {
		return gcprest.ResourcePath{}, false
	}

	parts := strings.Split(strings.TrimPrefix(path, projects), "/")
	if len(parts) < scopelessMinParts {
		return gcprest.ResourcePath{}, false
	}

	if parts[1] != resourceTargetHTTPProxies && parts[1] != resourceTargetHTTPSProxies {
		return gcprest.ResourcePath{}, false
	}

	rp := gcprest.ResourcePath{
		Project:      parts[0],
		Scope:        gcprest.ScopeGlobal,
		ResourceType: parts[1],
		ResourceName: parts[2],
	}
	if len(parts) > scopelessMinParts {
		rp.Action = parts[3]
	}

	return rp, true
}

// scopelessMinParts is the segment count of a scopeless-global named-resource
// path (project / type / name).
const scopelessMinParts = 3
