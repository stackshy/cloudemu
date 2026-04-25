// Package virtualmachines serves Azure ARM Microsoft.Compute/virtualMachines
// requests against a CloudEmu compute driver. Real azure-sdk-for-go clients
// configured with a custom endpoint hit this handler the same way they hit
// management.azure.com.
//
// Supported operations (instance lifecycle parity with AWS EC2):
//
//	PUT    .../virtualMachines/{name}        — CreateOrUpdate
//	GET    .../virtualMachines/{name}        — Get
//	GET    .../virtualMachines               — List in resource group
//	GET    .../providers/.../virtualMachines — List in subscription
//	DELETE .../virtualMachines/{name}        — Delete
//	POST   .../virtualMachines/{name}/start  — Start
//	POST   .../virtualMachines/{name}/powerOff — Stop
//	POST   .../virtualMachines/{name}/restart — Restart
//
// Less-used operations (capture, deallocate, instance view, redeploy, etc.)
// are not yet wired and will return 501 Not Implemented.
package virtualmachines

import (
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// providerName is the ARM provider this handler serves.
const providerName = "Microsoft.Compute"

// resourceType is the ARM resource type this handler serves.
const resourceType = "virtualMachines"

// Handler serves ARM JSON requests for Microsoft.Compute/virtualMachines.
type Handler struct {
	compute computedriver.Compute
}

// New returns a virtualMachines handler backed by c.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

// Matches returns true for ARM URLs targeting Microsoft.Compute/virtualMachines.
// The match is loose enough to accept both subscription-scoped and resource-
// group-scoped paths, but strict enough to leave other ARM providers alone so
// future services (storage, network) can register their own handler.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	return rp.ResourceType == resourceType
}

// ServeHTTP routes the request to the matching operation. Unrecognized
// combinations of (method, sub-resource) return 501 so misuse is visible
// rather than swallowed.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch {
	case rp.SubResource != "":
		h.serveAction(w, r, rp)
	case rp.ResourceName != "":
		h.serveResource(w, r, rp)
	default:
		h.serveCollection(w, r, rp)
	}
}

//nolint:gocritic // rp travels through the dispatch chain once per request
func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, rp)
	case http.MethodGet:
		h.get(w, r, rp)
	case http.MethodDelete:
		h.delete(w, r, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp travels through the dispatch chain once per request
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method == http.MethodGet {
		h.list(w, r, rp)
		return
	}

	writeNotImplemented(w, r.Method+" "+r.URL.Path)
}

//nolint:gocritic // rp travels through the dispatch chain once per request
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case "start":
		h.start(w, r, rp)
	case "poweroff", "deallocate":
		h.powerOff(w, r, rp)
	case "restart":
		h.restart(w, r, rp)
	default:
		writeNotImplemented(w, "action: "+rp.SubResource)
	}
}

func writeNotImplemented(w http.ResponseWriter, what string) {
	azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "not implemented: "+what)
}
