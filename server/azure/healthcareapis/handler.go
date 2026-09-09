// Package healthcareapis serves the Azure Health Data Services ARM API
// (Microsoft.HealthcareApis/workspaces plus the nested workspaces/{workspace}/
// fhirservices and workspaces/{workspace}/dicomservices child resources). Real
// armhealthcareapis WorkspacesClient / FhirServicesClient / DicomServicesClient
// requests hit this handler the same way they hit management.azure.com.
//
// Real Azure runs workspace and child CreateOrUpdate/Delete as long-running
// operations; the emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded, so there is no LRO plumbing to wire.
package healthcareapis

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/healthcareapis"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName     = "Microsoft.HealthcareApis"
	workspaceType    = "workspaces"
	fhirSegment      = "fhirservices"
	dicomSegment     = "dicomservices"
	workspaceArmType = providerName + "/" + workspaceType
	fhirArmType      = workspaceArmType + "/" + fhirSegment
	dicomArmType     = workspaceArmType + "/" + dicomSegment
)

// Store is the minimal healthcareapis backend the handler needs.
// *healthcareapis.Mock satisfies it.
type Store interface {
	CreateOrUpdateWorkspace(
		ctx context.Context, sub, rg, name, location string, in *healthcareapis.WorkspaceInput,
	) (healthcareapis.Workspace, bool, error)
	GetWorkspace(ctx context.Context, sub, rg, name string) (healthcareapis.Workspace, error)
	DeleteWorkspace(ctx context.Context, sub, rg, name string) (bool, error)
	ListWorkspacesByResourceGroup(ctx context.Context, sub, rg string) ([]healthcareapis.Workspace, error)
	ListWorkspacesBySubscription(ctx context.Context, sub string) ([]healthcareapis.Workspace, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error

	CreateOrUpdateFhir(
		ctx context.Context, sub, rg, workspace, name, location string, in *healthcareapis.FhirInput,
	) (healthcareapis.FhirService, bool, error)
	GetFhir(ctx context.Context, sub, rg, workspace, name string) (healthcareapis.FhirService, error)
	DeleteFhir(ctx context.Context, sub, rg, workspace, name string) (bool, error)
	ListFhirByWorkspace(ctx context.Context, sub, rg, workspace string) ([]healthcareapis.FhirService, error)

	CreateOrUpdateDicom(
		ctx context.Context, sub, rg, workspace, name, location string, in *healthcareapis.DicomInput,
	) (healthcareapis.DicomService, bool, error)
	GetDicom(ctx context.Context, sub, rg, workspace, name string) (healthcareapis.DicomService, error)
	DeleteDicom(ctx context.Context, sub, rg, workspace, name string) (bool, error)
	ListDicomByWorkspace(ctx context.Context, sub, rg, workspace string) ([]healthcareapis.DicomService, error)
}

// Handler serves Microsoft.HealthcareApis/workspaces (and nested fhir/dicom
// services) ARM requests.
type Handler struct {
	store Store
}

// New returns a healthcareapis handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a healthcareapis ARM URL. The provider and
// type are matched case-insensitively.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, workspaceType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// A "fhirservices" / "dicomservices" sub-resource routes to the nested child
	// surface; any other sub-resource is unknown (workspaces expose no POST
	// actions in scope).
	switch {
	case rp.SubResource == "":
		h.serveWorkspace(w, r, &rp)
	case strings.EqualFold(rp.SubResource, fhirSegment):
		h.serveFhir(w, r, &rp)
	case strings.EqualFold(rp.SubResource, dicomSegment):
		h.serveDicom(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown sub-resource "+rp.SubResource)
	}
}

// PurgeResourceGroup deletes every healthcareapis workspace and child service under
// sub/rg so a resource-group delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// ---- workspace ----

func (h *Handler) serveWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listWorkspaces(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createWorkspace(w, r, rp)
	case http.MethodPatch:
		h.updateWorkspace(w, r, rp)
	case http.MethodGet:
		h.getWorkspace(w, r, rp)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req workspaceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := workspaceInputFromRequest(&req)

	ws, created, err := h.store.CreateOrUpdateWorkspace(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), toWorkspaceResponse(&ws))
}

// updateWorkspace applies an ARM PATCH: only the supplied fields are overlaid onto
// the stored workspace; the immutable location and computed fields are preserved. A
// PATCH on a missing workspace is a 404.
func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.GetWorkspace(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req workspaceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := workspaceInputFromRequest(&req)

	ws, _, err := h.store.CreateOrUpdateWorkspace(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toWorkspaceResponse(&ws))
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ws, err := h.store.GetWorkspace(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toWorkspaceResponse(&ws))
}

func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteWorkspace(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveList(w, r, func() ([]healthcareapis.Workspace, error) {
		if rp.ResourceGroup != "" {
			return h.store.ListWorkspacesByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
		}

		return h.store.ListWorkspacesBySubscription(r.Context(), rp.Subscription)
	}, toWorkspaceResponse)
}

// ---- child services (fhir / dicom) ----

// childBinding adapts the typed backend of one child-service kind to the shared
// wire CRUD verbs. Req is the wire request body, T the stored value and R the wire
// response. create doubles as both the PUT and PATCH backend (the driver merges on
// nil), so a PATCH reuses it with the stored location.
type childBinding[Req, T, R any] struct {
	create         func(ctx context.Context, sub, rg, ws, name, location string, req *Req) (T, bool, error)
	get            func(ctx context.Context, sub, rg, ws, name string) (T, error)
	remove         func(ctx context.Context, sub, rg, ws, name string) (bool, error)
	reqLocation    func(*Req) string
	storedLocation func(*T) string
	project        func(*T) R
}

// serveNamed routes the verbs of a single named child resource.
func (b childBinding[Req, T, R]) serveNamed(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceAction != "" {
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResourceAction)
		return
	}

	switch r.Method {
	case http.MethodPut:
		b.put(w, r, rp)
	case http.MethodPatch:
		b.patch(w, r, rp)
	case http.MethodGet:
		b.read(w, r, rp)
	case http.MethodDelete:
		b.del(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (b childBinding[Req, T, R]) put(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req Req
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	v, created, err := b.create(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, b.reqLocation(&req), &req)
	if err != nil {
		writeChildErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), b.project(&v))
}

// patch applies an ARM PATCH: a missing resource is a 404, and the stored location
// is preserved (location is immutable in real Azure).
func (b childBinding[Req, T, R]) patch(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := b.get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req Req
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	v, _, err := b.create(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, b.storedLocation(&existing), &req)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, b.project(&v))
}

func (b childBinding[Req, T, R]) read(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	v, err := b.get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, b.project(&v))
}

func (b childBinding[Req, T, R]) del(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := b.remove(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

// ---- fhir ----

// fhirBinding wires the FHIR store methods into the generic child CRUD verbs.
//
// same generic childBinding for the two sibling child-service kinds; the
// structural symmetry is inherent to the generic, not incidental duplication.
//
//nolint:dupl // fhirBinding and dicomBinding are parallel instantiations of the
func (h *Handler) fhirBinding() childBinding[fhirRequest, healthcareapis.FhirService, fhirResponse] {
	return childBinding[fhirRequest, healthcareapis.FhirService, fhirResponse]{
		create: func(
			ctx context.Context, sub, rg, ws, name, location string, req *fhirRequest,
		) (healthcareapis.FhirService, bool, error) {
			in := fhirInputFromRequest(req)

			return h.store.CreateOrUpdateFhir(ctx, sub, rg, ws, name, location, &in)
		},
		get:            h.store.GetFhir,
		remove:         h.store.DeleteFhir,
		reqLocation:    func(req *fhirRequest) string { return req.Location },
		storedLocation: func(f *healthcareapis.FhirService) string { return f.Location },
		project:        toFhirResponse,
	}
}

func (h *Handler) serveFhir(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" && rp.SubResourceAction == "" {
		serveList(w, r, func() ([]healthcareapis.FhirService, error) {
			return h.store.ListFhirByWorkspace(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
		}, toFhirResponse)

		return
	}

	h.fhirBinding().serveNamed(w, r, rp)
}

// ---- dicom ----

// dicomBinding mirrors fhirBinding for the DICOM kind.
//
// same generic childBinding for the two sibling child-service kinds; the
// structural symmetry is inherent to the generic, not incidental duplication.
//
//nolint:dupl // fhirBinding and dicomBinding are parallel instantiations of the
func (h *Handler) dicomBinding() childBinding[dicomRequest, healthcareapis.DicomService, dicomResponse] {
	return childBinding[dicomRequest, healthcareapis.DicomService, dicomResponse]{
		create: func(
			ctx context.Context, sub, rg, ws, name, location string, req *dicomRequest,
		) (healthcareapis.DicomService, bool, error) {
			in := dicomInputFromRequest(req)

			return h.store.CreateOrUpdateDicom(ctx, sub, rg, ws, name, location, &in)
		},
		get:            h.store.GetDicom,
		remove:         h.store.DeleteDicom,
		reqLocation:    func(req *dicomRequest) string { return req.Location },
		storedLocation: func(d *healthcareapis.DicomService) string { return d.Location },
		project:        toDicomResponse,
	}
}

func (h *Handler) serveDicom(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" && rp.SubResourceAction == "" {
		serveList(w, r, func() ([]healthcareapis.DicomService, error) {
			return h.store.ListDicomByWorkspace(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
		}, toDicomResponse)

		return
	}

	h.dicomBinding().serveNamed(w, r, rp)
}

// listEnvelope is the ARM single-page list body shared by every list surface.
type listEnvelope[R any] struct {
	Value []R `json:"value"`
}

// serveList handles a GET collection request: it fetches the items via fetch,
// projects each onto its wire form via project and writes the ARM list envelope.
// A non-GET method is rejected.
func serveList[T, R any](
	w http.ResponseWriter, r *http.Request, fetch func() ([]T, error), project func(*T) R,
) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := fetch()
	if err != nil {
		writeChildErr(w, err)
		return
	}

	out := listEnvelope[R]{Value: make([]R, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, project(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// ---- helpers ----

// createdStatus returns 201 for a fresh create, 200 for an update.
func createdStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}

// writeDeleteStatus writes the idempotent ARM DELETE result: 200 when the resource
// existed, 204 when it did not.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeChildErr maps a child create error, translating a missing parent workspace
// (NotFound) into the ARM ParentResourceNotFound 404 real Azure returns.
func writeChildErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteCErr(w, err)
}
