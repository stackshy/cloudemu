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
//	POST   .../virtualMachines/{name}/retrieveBootDiagnosticsData — boot-diagnostics URIs
//	GET    .../virtualMachines/{name}/bootDiagnostics/serialConsoleLog — serial-log bytes
//
// Less-used operations (capture, deallocate, instance view, redeploy, etc.)
// are not yet wired and will return 501 Not Implemented.
package virtualmachines

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// providerName is the ARM provider this handler serves.
const providerName = "Microsoft.Compute"

// resourceType is the ARM resource type this handler serves.
const resourceType = "virtualMachines"

// resourceTypeScaleSets is the ARM resource type for VM Scale Sets, served by
// the same handler when the backing driver exposes scale-set methods.
const resourceTypeScaleSets = "virtualMachineScaleSets"

// resourceTypeLocations is the resource type used for async operation
// status endpoints (Microsoft.Compute/locations/{loc}/operationStatuses/{id}).
const resourceTypeLocations = "locations"

// Handler serves ARM JSON requests for Microsoft.Compute/virtualMachines.
type Handler struct {
	compute computedriver.Compute
	// net is the networking driver used to validate that a VM's networkProfile
	// references an existing NIC. It is optional: a nil net (or one that does
	// not implement AzureNetworkInterfaces) skips the existence check.
	net netdriver.Networking
}

// New returns a virtualMachines handler backed by c. net is the networking
// driver used to validate networkProfile NIC references; pass nil to disable
// the check (e.g. when no networking driver is wired).
func New(c computedriver.Compute, net netdriver.Networking) *Handler {
	return &Handler{compute: c, net: net}
}

// Matches returns true for ARM URLs targeting Microsoft.Compute/virtualMachines
// or our async-operation status endpoints.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	switch rp.ResourceType {
	case resourceType, resourceTypeScaleSets, resourceTypeLocations:
		return true
	}

	return false
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

	if rp.ResourceType == resourceTypeLocations && rp.SubResource == "operationStatuses" {
		serveOperationStatus(w, r, rp)
		return
	}

	if rp.ResourceType == resourceTypeScaleSets {
		h.serveScaleSet(w, r, rp)
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

// serveOperationStatus answers the async-operation polling endpoint that the
// Azure SDK hits after we 202-Accepted a long-running op. Since our backing
// driver is synchronous, we always return Succeeded.
//
//nolint:gocritic // rp is a request-scoped value
func serveOperationStatus(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{
		"name":      rp.SubResourceName,
		"status":    "Succeeded",
		"startTime": "2024-01-01T00:00:00Z",
		"endTime":   "2024-01-01T00:00:01Z",
	})
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
	// Boot-diagnostics serial-log download is a GET sub-path, unlike the POST
	// lifecycle/retrieve actions.
	if strings.EqualFold(rp.SubResource, "bootDiagnostics") {
		h.serveBootDiagnostics(w, r, rp)
		return
	}

	// instanceView is a GET sub-resource, not a POST action.
	if strings.EqualFold(rp.SubResource, "instanceView") {
		if r.Method != http.MethodGet {
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
			return
		}

		h.instanceView(w, r, rp)

		return
	}

	if r.Method != http.MethodPost {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	h.servePostAction(w, r, rp)
}

// servePostAction dispatches the POST sub-resource actions on a named VM.
//
//nolint:gocritic // rp travels through the dispatch chain once per request
func (h *Handler) servePostAction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	switch strings.ToLower(rp.SubResource) {
	case "start":
		h.start(w, r, rp)
	case "poweroff":
		h.powerOff(w, r, rp)
	case "deallocate":
		h.deallocate(w, r, rp)
	case "restart":
		h.restart(w, r, rp)
	case "generalize":
		h.generalize(w, r, rp)
	case "capture":
		h.capture(w, r, rp)
	case "retrievebootdiagnosticsdata":
		h.retrieveBootDiagnosticsData(w, r, rp)
	default:
		writeNotImplemented(w, "action: "+rp.SubResource)
	}
}

// serveBootDiagnostics serves the serial-log blob download that
// retrieveBootDiagnosticsData points its serialConsoleLogBlobUri at.
//
//nolint:gocritic // rp travels through the dispatch chain once per request
func (h *Handler) serveBootDiagnostics(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method == http.MethodGet && strings.EqualFold(rp.SubResourceName, "serialConsoleLog") {
		h.serialConsoleLog(w, r, rp)
		return
	}

	writeNotImplemented(w, r.Method+" "+r.URL.Path)
}

func writeNotImplemented(w http.ResponseWriter, what string) {
	azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "not implemented: "+what)
}
