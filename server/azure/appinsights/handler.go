// Package appinsights serves the Azure Application Insights ARM control-plane API
// (Microsoft.Insights/components). Real azure-sdk-for-go armapplicationinsights
// and the Terraform azurerm provider drive this surface the same way they hit
// management.azure.com.
//
// A component's InstrumentationKey and AppId are GUID-shaped values Azure
// computes once at create and returns verbatim on every read (the REST contract
// forbids specifying a different value on a PUT). This handler generates them
// deterministically and stores them once, so they are stable across repeated
// GETs — the property that keeps a Terraform plan drift-free. ConnectionString is
// derived from the stored key, region and app id. kind is a top-level field the
// generic property-echo overlay cannot reach, so it is modeled explicitly here.
//
// The handler is self-contained with no backing driver (its state is
// resource-group-scoped ARM containers, like Event Hubs and Synapse), so it is
// always registered. It shares the microsoft.insights provider with the Azure
// Monitor handler but claims the disjoint "components" resource type, so their
// registration order is unconstrained.
package appinsights

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// Handler serves Microsoft.Insights/components requests over an in-memory store.
type Handler struct {
	store *store
}

// New returns an Application Insights control-plane handler.
func New() *Handler { return &Handler{store: newStore()} }

// Matches reports whether r targets a Microsoft.Insights/components URL. The
// provider is matched case-insensitively because armapplicationinsights emits
// "Microsoft.Insights" while other tooling emits the lowercase form.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)

	return ok && strings.EqualFold(rp.Provider, providerName) && strings.EqualFold(rp.ResourceType, typeComponent)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.list(w, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp)
	case http.MethodPatch:
		h.patch(w, r, &rp)
	case http.MethodGet:
		h.get(w, &rp)
	case http.MethodDelete:
		h.delete(w, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// createOrUpdate handles PUT. On first create it computes and stores the
// instrumentation key, app id, tenant id and creation date; on a subsequent PUT
// those computed values are carried forward unchanged (they can never be
// re-specified), while location, kind, tags and the writable properties are
// replaced.
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req componentRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	existing, existed := h.store.get(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	cs := &componentState{
		Subscription:  rp.Subscription,
		ResourceGroup: rp.ResourceGroup,
		Name:          rp.ResourceName,
		Location:      req.Location,
		Kind:          req.Kind,
		Tags:          cloneTags(req.Tags),
		Props:         applyDefaults(req.Properties),
	}

	if existed {
		cs.InstrumentationKey = existing.InstrumentationKey
		cs.AppID = existing.AppID
		cs.TenantID = existing.TenantID
		cs.CreationDate = existing.CreationDate
	} else {
		id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeComponent, rp.ResourceName)
		cs.InstrumentationKey = newInstrumentationKey(id)
		cs.AppID = newAppID(id)
		cs.TenantID = newTenantID(rp.Subscription)
		cs.CreationDate = nowISO8601()
	}

	h.store.set(cs)

	status := http.StatusOK
	if !existed {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(cs))
}

// patch handles the ARM Update (HTTP PATCH): tags are replaced wholesale when a
// tags key is present, location and kind are preserved unless supplied, and the
// writable properties merge over the stored set. Update on a missing component is
// a 404, matching the real ARM Update contract.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, ok := h.store.get(rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "component "+rp.ResourceName+" not found")
		return
	}

	var req componentRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	merged := *existing
	merged.Props = mergePatchProps(existing.Props, req.Properties)

	if req.Location != "" {
		merged.Location = req.Location
	}

	if req.Kind != "" {
		merged.Kind = req.Kind
	}

	if req.Tags != nil {
		merged.Tags = cloneTags(req.Tags)
	}

	h.store.set(&merged)

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&merged))
}

func (h *Handler) get(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	cs, ok := h.store.get(rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "component "+rp.ResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(cs))
}

func (h *Handler) list(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	components := h.store.listBy(rp.Subscription, rp.ResourceGroup)

	out := componentListResponse{Value: make([]componentResponse, 0, len(components))}
	for _, cs := range components {
		out.Value = append(out.Value, toResponse(cs))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// delete removes a component. ARM DELETE is idempotent: a missing component
// returns 204 No Content, an existing one 200 OK.
func (h *Handler) delete(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	if h.store.delete(rp.Subscription, rp.ResourceGroup, rp.ResourceName) {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// PurgeResourceGroup deletes every component under sub/rg, so a resource-group
// delete cascades into them (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(_ context.Context, subscription, resourceGroup string) error {
	h.store.purge(subscription, resourceGroup)
	return nil
}
