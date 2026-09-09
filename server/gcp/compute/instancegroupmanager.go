package compute

import (
	"net/http"
	"strconv"
	"strings"

	gcecompute "github.com/stackshy/cloudemu/v2/providers/gcp/compute"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// migBackend is the GCP-local capability the GCE Mock implements to store zonal
// managed instance groups (compute#instanceGroupManager). Reached via a type
// assertion so the shared compute driver interface stays unchanged, mirroring
// the volumeResizer / resourceLabelMutator pattern used for disks/images.
type migBackend interface {
	CreateInstanceGroupManagerGCP(igm gcecompute.InstanceGroupManager) error
	GetInstanceGroupManagerGCP(zone, name string) (gcecompute.InstanceGroupManager, bool)
	ListInstanceGroupManagersGCP(zone string) []gcecompute.InstanceGroupManager
	AllInstanceGroupManagersGCP() []gcecompute.InstanceGroupManager
	DeleteInstanceGroupManagerGCP(zone, name string) error
	ResizeInstanceGroupManagerGCP(zone, name string, size int) error
}

// migRequest mirrors the subset of compute#instanceGroupManager we accept on
// insert. targetSize is a flexInt so both the typed client (quoted) and the
// Terraform provider (bare number) encodings decode.
type migRequest struct {
	Name             string  `json:"name"`
	BaseInstanceName string  `json:"baseInstanceName,omitempty"`
	InstanceTemplate string  `json:"instanceTemplate,omitempty"`
	TargetSize       flexInt `json:"targetSize,omitempty"`
}

// migResponse mirrors the subset of compute#instanceGroupManager we return.
type migResponse struct {
	Kind              string             `json:"kind"`
	ID                string             `json:"id"`
	CreationTimestamp string             `json:"creationTimestamp,omitempty"`
	Name              string             `json:"name"`
	Zone              string             `json:"zone"`
	BaseInstanceName  string             `json:"baseInstanceName,omitempty"`
	InstanceTemplate  string             `json:"instanceTemplate,omitempty"`
	InstanceGroup     string             `json:"instanceGroup"`
	TargetSize        int                `json:"targetSize"`
	Fingerprint       string             `json:"fingerprint,omitempty"`
	CurrentActions    *migCurrentActions `json:"currentActions,omitempty"`
	Status            *migStatus         `json:"status,omitempty"`
	SelfLink          string             `json:"selfLink"`
}

// migCurrentActions is compute#instanceGroupManagerActionsSummary. The emulator
// applies resizes synchronously, so every target is "none" (stable) — none
// equals the target size and every transient counter is zero.
type migCurrentActions struct {
	None                   int `json:"none"`
	Creating               int `json:"creating"`
	CreatingWithoutRetries int `json:"creatingWithoutRetries"`
	Deleting               int `json:"deleting"`
	Abandoning             int `json:"abandoning"`
	Restarting             int `json:"restarting"`
	Refreshing             int `json:"refreshing"`
	Verifying              int `json:"verifying"`
	Recreating             int `json:"recreating"`
}

type migStatus struct {
	IsStable bool `json:"isStable"`
}

type migListResponse struct {
	Kind     string        `json:"kind"`
	ID       string        `json:"id"`
	Items    []migResponse `json:"items"`
	SelfLink string        `json:"selfLink"`
}

// serveInstanceGroupManagersRoute dispatches the zonal instanceGroupManagers
// resource. Registered ahead of the compute-space fallback so first-match-wins
// keeps these paths here; disjoint from the load-balancing handler's
// instanceGroups collection, so registration order is unconstrained.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveInstanceGroupManagersRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	backend, ok := h.compute.(migBackend)
	if !ok {
		writeNotImplemented(w, "instanceGroupManagers")
		return
	}

	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertMIG(w, r, rp, backend)
		case http.MethodGet:
			h.listMIGs(w, r, rp, backend)
		default:
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
		}

		return
	}

	if r.Method == http.MethodPost && rp.Action != "" {
		h.serveMIGAction(w, r, rp, backend)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getMIG(w, r, rp, backend)
	case http.MethodDelete:
		h.deleteMIG(w, r, rp, backend)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

// serveMIGAction routes the POST MIG verbs. resize is the real zonal-MIG method
// (size is a query parameter); setTargetSize is accepted as a body-carried
// alias for clients that prefer it.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveMIGAction(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	switch strings.ToLower(rp.Action) {
	case actionResize:
		h.resizeMIG(w, r, rp, backend)
	case "settargetsize":
		h.setMIGTargetSize(w, r, rp, backend)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	if rp.Scope != gcprest.ScopeZones {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instance group managers must be created in a zone")
		return
	}

	var req migRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instance group manager name required")
		return
	}

	baseName := req.BaseInstanceName
	if baseName == "" {
		baseName = req.Name
	}

	err := backend.CreateInstanceGroupManagerGCP(gcecompute.InstanceGroupManager{
		Name:             req.Name,
		Zone:             rp.ScopeName,
		TargetSize:       int(req.TargetSize),
		BaseInstanceName: baseName,
		InstanceTemplate: lastSegment(req.InstanceTemplate),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	igm, ok := backend.GetInstanceGroupManagerGCP(rp.ScopeName, rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"The resource 'instanceGroupManagers/"+rp.ResourceName+"' was not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toMIGResponse(&igm, rp.Project, hostFromRequest(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) listMIGs(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	igms := backend.ListInstanceGroupManagersGCP(rp.ScopeName)
	host := hostFromRequest(r)
	out := make([]migResponse, 0, len(igms))

	for i := range igms {
		if !gcprest.NameMatches(r.URL.Query().Get("filter"), igms[i].Name) {
			continue
		}

		out = append(out, toMIGResponse(&igms[i], rp.Project, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, migListResponse{
		Kind:     "compute#instanceGroupManagerList",
		ID:       "projects/" + rp.Project + "/zones/" + rp.ScopeName + "/instanceGroupManagers",
		Items:    out,
		SelfLink: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "instanceGroupManagers", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	if _, ok := backend.GetInstanceGroupManagerGCP(rp.ScopeName, rp.ResourceName); !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"The resource 'instanceGroupManagers/"+rp.ResourceName+"' was not found")

		return
	}

	if err := backend.DeleteInstanceGroupManagerGCP(rp.ScopeName, rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// resizeMIG handles POST .../instanceGroupManagers/{name}/resize?size=N, the
// real zonal MIG resize where size is a required query parameter.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) resizeMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	raw := r.URL.Query().Get("size")
	if raw == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "size query parameter required")
		return
	}

	size, err := strconv.Atoi(raw)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "size must be an integer")
		return
	}

	h.applyMIGResize(w, r, rp, backend, size)
}

// migTargetSizeRequest is the setTargetSize body alias.
type migTargetSizeRequest struct {
	TargetSize flexInt `json:"targetSize,omitempty"`
}

// setMIGTargetSize handles POST .../instanceGroupManagers/{name}/setTargetSize
// with the target in the body — a convenience alias over resize.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setMIGTargetSize(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	var req migTargetSizeRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	h.applyMIGResize(w, r, rp, backend, int(req.TargetSize))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) applyMIGResize(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend, size int) {
	if err := backend.ResizeInstanceGroupManagerGCP(rp.ScopeName, rp.ResourceName, size); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", rp.ResourceName, "resize")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// migScopedList is one zone's bucket in an aggregated MIG list.
type migScopedList struct {
	InstanceGroupManagers []migResponse      `json:"instanceGroupManagers,omitempty"`
	Warning               *scopedListWarning `json:"warning,omitempty"`
}

type migAggregatedListResponse struct {
	Kind     string                   `json:"kind"`
	ID       string                   `json:"id"`
	Items    map[string]migScopedList `json:"items"`
	SelfLink string                   `json:"selfLink"`
}

// aggregatedListMIGs handles GET /aggregated/instanceGroupManagers, grouping
// every MIG by its "zones/{zone}" scope.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListMIGs(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	backend, ok := h.compute.(migBackend)
	if !ok {
		writeNotImplemented(w, "instanceGroupManagers")
		return
	}

	igms := backend.AllInstanceGroupManagersGCP()
	host := hostFromRequest(r)
	items := make(map[string]migScopedList)

	for i := range igms {
		key := "zones/" + igms[i].Zone
		bucket := items[key]
		bucket.InstanceGroupManagers = append(bucket.InstanceGroupManagers, toMIGResponse(&igms[i], rp.Project, host))
		items[key] = bucket
	}

	gcprest.WriteJSON(w, http.StatusOK, migAggregatedListResponse{
		Kind:     "compute#instanceGroupManagerAggregatedList",
		ID:       "projects/" + rp.Project + "/aggregated/instanceGroupManagers",
		Items:    items,
		SelfLink: strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + rp.Project + "/aggregated/instanceGroupManagers",
	})
}

// toMIGResponse maps a stored MIG to compute#instanceGroupManager wire JSON.
// The instanceGroup selfLink points at the same-named zonal instanceGroups
// resource, as real GCP does (the managed group owns an unmanaged instance
// group of the same name).
func toMIGResponse(igm *gcecompute.InstanceGroupManager, project, host string) migResponse {
	return migResponse{
		Kind:              "compute#instanceGroupManager",
		ID:                numericID(igm.Zone + "/" + igm.Name),
		CreationTimestamp: igm.CreatedAt,
		Name:              igm.Name,
		Zone:              strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + project + "/zones/" + igm.Zone,
		BaseInstanceName:  igm.BaseInstanceName,
		InstanceTemplate:  igm.InstanceTemplate,
		InstanceGroup:     gcprest.SelfLink(host, project, gcprest.ScopeZones, igm.Zone, "instanceGroups", igm.Name),
		TargetSize:        igm.TargetSize,
		CurrentActions:    &migCurrentActions{None: igm.TargetSize},
		Status:            &migStatus{IsStable: true},
		SelfLink:          gcprest.SelfLink(host, project, gcprest.ScopeZones, igm.Zone, "instanceGroupManagers", igm.Name),
	}
}
