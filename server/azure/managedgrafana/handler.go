// Package managedgrafana serves the Azure Managed Grafana ARM API
// (Microsoft.Dashboard/grafana). Real armdashboard GrafanaClient requests hit
// this handler the same way they hit management.azure.com.
//
// Real Azure runs CreateOrUpdate/Update/Delete as long-running operations; the
// emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded, so there is no LRO plumbing to wire.
package managedgrafana

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedgrafana"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.Dashboard"
	resourceType = "grafana"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal grafana backend the handler needs.
// *managedgrafana.Mock satisfies it.
type Store interface {
	CreateOrUpdate(
		ctx context.Context, sub, rg, name, location string, in *managedgrafana.Input,
	) (managedgrafana.Grafana, bool, error)
	Get(ctx context.Context, sub, rg, name string) (managedgrafana.Grafana, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]managedgrafana.Grafana, error)
	ListBySubscription(ctx context.Context, sub string) ([]managedgrafana.Grafana, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.Dashboard/grafana ARM requests.
type Handler struct {
	store Store
}

// New returns a grafana handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a grafana ARM URL. The provider and type are
// matched case-insensitively because SDK URL templates and hand-written tooling
// differ in casing.
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

// PurgeResourceGroup deletes every grafana resource under sub/rg so a
// resource-group delete cascades into its grafana resources.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req grafanaRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := inputFromRequest(&req)

	s, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&s))
}

// update applies an ARM PATCH: only the supplied fields are overlaid onto the
// stored resource; the computed ids, the immutable location and unmentioned
// fields are preserved. A PATCH on a missing resource is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req grafanaRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := mergePatch(&req)

	s, _, err := h.store.CreateOrUpdate(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&s))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	s, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&s))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armdashboard client accepts both.
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
		items []managedgrafana.Grafana
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listResponse{Value: make([]grafanaResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// inputFromRequest builds a create/update Input from a PUT body. Pointer fields
// are carried through verbatim so an absent field falls back to the stored (or
// default) value in the driver.
func inputFromRequest(req *grafanaRequest) managedgrafana.Input {
	in := managedgrafana.Input{
		Tags:     req.Tags,
		Sku:      toDriverSku(req.Sku),
		Identity: toDriverIdentity(req.Identity),
	}

	if req.Properties != nil {
		in.PublicNetworkAccess = req.Properties.PublicNetworkAccess
		in.ZoneRedundancy = req.Properties.ZoneRedundancy
		in.APIKey = req.Properties.APIKey
		in.DeterministicOutIP = req.Properties.DeterministicOutboundIP
		in.DomainNameLabelScope = req.Properties.DomainNameLabelScope
		in.GrafanaMajorVersion = req.Properties.GrafanaMajorVersion

		if req.Properties.GrafanaIntegrations != nil {
			ids := integrationIDs(req.Properties.GrafanaIntegrations)
			in.MonitorWorkspaceIDs = &ids
		}
	}

	return in
}

// mergePatch builds the Input for a PATCH: only the fields present in the request
// carry a non-nil pointer, so the driver preserves everything the PATCH did not
// mention. It reuses inputFromRequest — a PATCH body is structurally the same as
// a PUT body, and every field is optional.
func mergePatch(req *grafanaRequest) managedgrafana.Input {
	return inputFromRequest(req)
}
