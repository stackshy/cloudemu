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
//	GET    /compute/v1/projects/{p}/zones/{z}/operations/{name}      — get operation (always DONE)
//
// Less-used surfaces (aggregated list, snapshots, disks, images) are not yet
// wired and return 501 Not Implemented.
package compute

import (
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

// Resource type names used in URL routing.
const (
	resourceInstances  = "instances"
	resourceOperations = "operations"
	resourceDisks      = "disks"
	resourceSnapshots  = "snapshots"
	resourceImages     = "images"
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
	case resourceInstances, resourceOperations, resourceDisks, resourceSnapshots, resourceImages:
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

	if rp.ResourceType == resourceOperations {
		serveOperations(w, r, rp)
		return
	}

	if rp.ResourceType == resourceDisks {
		h.serveDisksRoute(w, r, rp)
		return
	}

	if rp.ResourceType == resourceSnapshots {
		h.serveSnapshotsRoute(w, r, rp)
		return
	}

	if rp.ResourceType == resourceImages {
		h.serveImagesRoute(w, r, rp)
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
	if r.Method != http.MethodPost {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	switch strings.ToLower(rp.Action) {
	case "start":
		h.startInstance(w, r, rp)
	case "stop":
		h.stopInstance(w, r, rp)
	case "reset":
		h.resetInstance(w, r, rp)
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
		writeNotImplemented(w, "operations list")
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
