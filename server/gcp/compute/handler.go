// Package compute serves GCP Compute Engine REST API requests against a
// CloudEmu compute driver. Real cloud.google.com/go/compute clients
// configured with a custom endpoint hit this handler the same way they hit
// compute.googleapis.com.
//
// Supported operations (instance lifecycle parity with AWS EC2):
//
//	POST   /compute/v1/projects/{p}/zones/{z}/instances              — insert
//	GET    /compute/v1/projects/{p}/zones/{z}/instances/{name}       — get
//	GET    /compute/v1/projects/{p}/zones/{z}/instances              — list
//	DELETE /compute/v1/projects/{p}/zones/{z}/instances/{name}       — delete
//	POST   /compute/v1/projects/{p}/zones/{z}/instances/{name}/start — start
//	POST   /compute/v1/projects/{p}/zones/{z}/instances/{name}/stop  — stop
//	POST   /compute/v1/projects/{p}/zones/{z}/instances/{name}/reset — reset
//	POST   /compute/v1/projects/{p}/zones/{z}/instances/{name}/{verb} — setLabels/setMetadata/setTags/setMachineType/attachDisk/detachDisk
//	GET    /compute/v1/projects/{p}/aggregated/instances             — aggregatedList (grouped by zone)
//	GET    /compute/v1/projects/{p}/zones/{z}/operations/{name}      — get operation (always DONE)
package compute

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Resource type names used in URL routing.
const (
	resourceInstances  = "instances"
	resourceOperations = "operations"
	resourceDisks      = "disks"
	resourceSnapshots  = "snapshots"
	resourceImages     = "images"
	resourceMachineTyp = "machineTypes"
)

// Handler serves GCP Compute Engine REST requests for instances and zone
// operations.
type Handler struct {
	compute computedriver.Compute
}

// New returns a Compute handler backed by c.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

// Matches returns true for /compute/v1/projects/... URLs targeting instances
// or operations resources. Other resource types fall through so future
// handlers (disks, networks) can register independently.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rp.ResourceType {
	case resourceInstances, resourceOperations, resourceDisks, resourceSnapshots, resourceImages, resourceMachineTyp:
		return true
	}

	return false
}

// ServeHTTP routes the parsed path to the matching operation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := gcprest.ParsePath(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed path")
		return
	}

	if rp.Scope == gcprest.ScopeAggregated {
		h.serveAggregated(w, r, rp)
		return
	}

	if h.routeResource(w, r, rp) {
		return
	}

	switch {
	case rp.Action != "":
		h.serveInstanceAction(w, r, rp)
	case rp.ResourceName != "":
		h.serveInstance(w, r, rp)
	default:
		h.serveInstanceCollection(w, r, rp)
	}
}

// serveAggregated handles the /aggregated/{type} scope (currently only
// instances, which gcloud uses when no zone is given).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveAggregated(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if r.Method == http.MethodGet && rp.ResourceType == resourceInstances {
		h.aggregatedListInstances(w, r, rp)
		return
	}

	writeNotImplemented(w, r.Method+" "+r.URL.Path)
}

// routeResource dispatches the non-instance resource types (operations, disks,
// snapshots, images, machineTypes), returning true when it handled the request.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) bool {
	switch rp.ResourceType {
	case resourceOperations:
		serveOperations(w, r, rp)
	case resourceDisks:
		h.serveDisksRoute(w, r, rp)
	case resourceSnapshots:
		h.serveSnapshotsRoute(w, r, rp)
	case resourceImages:
		h.serveImagesRoute(w, r, rp)
	case resourceMachineTyp:
		serveMachineTypesRoute(w, r, rp)
	default:
		return false
	}

	return true
}

//nolint:gocritic,dupl // rp is a request-scoped value; route shape is duplicate-by-design across resource types
func (h *Handler) serveSnapshotsRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertSnapshot(w, r, rp)
		case http.MethodGet:
			h.listSnapshots(w, r, rp)
		default:
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSnapshot(w, r, rp)
	case http.MethodDelete:
		h.deleteSnapshot(w, r, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; route shape is duplicate-by-design across resource types
func (h *Handler) serveImagesRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertImage(w, r, rp)
		case http.MethodGet:
			h.listImages(w, r, rp)
		default:
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getImage(w, r, rp)
	case http.MethodDelete:
		h.deleteImage(w, r, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic,dupl // rp is a request-scoped value; route shape is duplicate-by-design across resource types
func (h *Handler) serveDisksRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertDisk(w, r, rp)
		case http.MethodGet:
			h.listDisks(w, r, rp)
		default:
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getDisk(w, r, rp)
	case http.MethodDelete:
		h.deleteDisk(w, r, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, rp)
	case http.MethodDelete:
		h.deleteInstance(w, r, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveInstanceCollection(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	switch r.Method {
	case http.MethodPost:
		h.insertInstance(w, r, rp)
	case http.MethodGet:
		h.listInstances(w, r, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveInstanceAction(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	// getSerialPortOutput is the one instance action read with GET; the
	// lifecycle actions (start/stop/reset) are POSTs.
	if strings.EqualFold(rp.Action, "serialPort") {
		if r.Method != http.MethodGet {
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
			return
		}

		h.getSerialPortOutput(w, r, rp)

		return
	}

	if r.Method != http.MethodPost {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	h.dispatchInstanceVerb(w, r, rp)
}

// dispatchInstanceVerb routes the POST instance verbs (lifecycle + GCP-specific
// mutations) to their handlers.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) dispatchInstanceVerb(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	switch strings.ToLower(rp.Action) {
	case "start":
		h.startInstance(w, r, rp)
	case "stop":
		h.stopInstance(w, r, rp)
	case "reset":
		h.resetInstance(w, r, rp)
	case "setlabels":
		h.setLabels(w, r, rp)
	case "setmetadata":
		h.setMetadata(w, r, rp)
	case "settags":
		h.setTags(w, r, rp)
	case "setmachinetype":
		h.setMachineType(w, r, rp)
	case "attachdisk":
		h.attachDisk(w, r, rp)
	case "detachdisk":
		h.detachDisk(w, r, rp)
	default:
		writeNotImplemented(w, "action: "+rp.Action)
	}
}

// serveOperations handles GET on operations/{name}. Since our mock executes
// synchronously, every operation lookup returns DONE.
//
//nolint:gocritic // rp is a request-scoped value
func serveOperations(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if r.Method != http.MethodGet {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	if rp.ResourceName == "" {
		// The mock runs synchronously and retains no pending operations, so a
		// list is legitimately empty rather than unimplemented.
		host := hostFromRequest(r)
		gcprest.WriteJSON(w, http.StatusOK, map[string]any{
			"kind":     "compute#operationList",
			"id":       "projects/" + rp.Project + "/operations",
			"items":    []any{},
			"selfLink": gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "operations", ""),
		})

		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", strings.TrimPrefix(rp.ResourceName, "operation-"), "noop")
	// Preserve the original operation name so SDK clients matching on Name
	// still recognize the polled operation, but keep ID numeric (uint64).
	op.Name = rp.ResourceName

	gcprest.WriteJSON(w, http.StatusOK, op)
}

func writeNotImplemented(w http.ResponseWriter, what string) {
	gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "not implemented: "+what)
}

// hostFromRequest returns the scheme://host of the incoming request, so
// selfLink and targetLink in operations point back at the test server.
func hostFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}
