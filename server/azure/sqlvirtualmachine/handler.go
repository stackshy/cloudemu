// Package sqlvirtualmachine serves the Azure SQL Virtual Machine ARM API
// (Microsoft.SqlVirtualMachine/sqlVirtualMachines). Real armsqlvirtualmachine
// SQLVirtualMachinesClient requests hit this handler the same way they hit
// management.azure.com.
//
// Create/update/delete are long-running operations in real Azure, but every
// mutating response carries the resource inline with a terminal
// provisioningState of "Succeeded", so an SDK LRO poller completes on the first
// response — no operationStatuses plumbing to wire.
//
// The sqlVirtualMachineGroups and availabilityGroupListeners resources (the
// WSFC / Always-On availability-group surface) are out of scope for this
// handler; only the core sqlVirtualMachines resource is served.
package sqlvirtualmachine

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/sqlvirtualmachine"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.SqlVirtualMachine"
	resourceType = "sqlVirtualMachines"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal SQL-virtual-machine backend the handler needs.
// *sqlvirtualmachine.Mock satisfies it.
type Store interface {
	CreateOrUpdate(ctx context.Context, sub, rg, name string,
		in *sqlvirtualmachine.Input) (sqlvirtualmachine.Record, bool, error)
	Get(ctx context.Context, sub, rg, name string) (sqlvirtualmachine.Record, error)
	UpdateTags(ctx context.Context, sub, rg, name string, tags map[string]string) (sqlvirtualmachine.Record, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]sqlvirtualmachine.Record, error)
	ListBySubscription(ctx context.Context, sub string) ([]sqlvirtualmachine.Record, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.SqlVirtualMachine/sqlVirtualMachines ARM requests.
type Handler struct {
	store Store
}

// New returns a SQL-virtual-machine handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a sqlVirtualMachines ARM URL. The provider
// and type are matched case-insensitively because SDK URL templates and
// hand-written tooling differ in casing. The child sqlVirtualMachineGroups /
// availabilityGroupListeners resources use different type segments and so are
// not claimed here.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, resourceType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// A collection URL (no resource name) is a list — by resource group when the
	// path carried one, otherwise by subscription.
	if rp.ResourceName == "" {
		h.list(w, r, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp)
	case http.MethodPatch:
		h.update(w, r, &rp)
	case http.MethodGet:
		h.get(w, r, &rp)
	case http.MethodDelete:
		h.delete(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// PurgeResourceGroup deletes every SQL virtual machine under sub/rg so a
// resource-group delete cascades (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req armResource
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := sqlvirtualmachine.Input{Location: req.Location, Tags: req.Tags}
	if req.Properties != nil {
		in.Properties = *req.Properties
	}

	rec, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&rec))
}

// update applies an ARM PATCH: the tag set is replaced wholesale (an empty or
// absent tags map clears every tag), and the properties are left untouched. A
// PATCH on a missing resource is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req updateRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	rec, err := h.store.UpdateTags(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Tags)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&rec))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rec, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&rec))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a deleted resource returns 200 OK, a missing one
	// 204 No Content. The armsqlvirtualmachine BeginDelete poller accepts both as
	// terminal.
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		recs []sqlvirtualmachine.Record
		err  error
	)

	if rp.ResourceGroup != "" {
		recs, err = h.store.ListByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		recs, err = h.store.ListBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listResponse{Value: make([]armResource, 0, len(recs))}
	for i := range recs {
		out.Value = append(out.Value, toResponse(&recs[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// compile-time check that Handler satisfies the dispatch contract.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*Handler)(nil)
