// Package containerapps serves the Azure Container Apps ARM API
// (Microsoft.App/managedEnvironments and Microsoft.App/containerApps). Real
// armappcontainers ManagedEnvironmentsClient and ContainerAppsClient requests
// hit this handler the same way they hit management.azure.com.
//
// The SDK's create/delete are Begin* pollers. This handler answers them
// synchronously — a create returns 201/200 with a body whose provisioningState
// is already "Succeeded" and no Azure-AsyncOperation/Location header, and a
// delete returns 200/204 — so the poller terminates on its first poll and never
// hangs. This mirrors the Event Hubs and Service Bus control-plane handlers.
package containerapps

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/containerapps"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName      = "Microsoft.App"
	typeEnvironments  = "managedEnvironments"
	typeContainerApps = "containerApps"

	// subResourceRevisions is the sub-resource segment for a container app's
	// revisions (.../containerApps/{app}/revisions[/{rev}[/{action}]]).
	subResourceRevisions = "revisions"

	actionActivate   = "activate"
	actionDeactivate = "deactivate"
	actionRestart    = "restart"
)

// Store is the minimal Container Apps backend the handler needs.
// *containerapps.Mock satisfies it.
type Store interface {
	CreateOrUpdateEnvironment(
		ctx context.Context, sub, rg, name string, in containerapps.EnvironmentInput,
	) (containerapps.Environment, bool, error)
	GetEnvironment(ctx context.Context, sub, rg, name string) (containerapps.Environment, error)
	DeleteEnvironment(ctx context.Context, sub, rg, name string) (bool, error)
	ListEnvironmentsByResourceGroup(ctx context.Context, sub, rg string) ([]containerapps.Environment, error)
	ListEnvironmentsBySubscription(ctx context.Context, sub string) ([]containerapps.Environment, error)

	CreateOrUpdateApp(
		ctx context.Context, sub, rg, name string, in *containerapps.AppInput,
	) (containerapps.ContainerApp, bool, error)
	GetApp(ctx context.Context, sub, rg, name string) (containerapps.ContainerApp, error)
	DeleteApp(ctx context.Context, sub, rg, name string) (bool, error)
	ListAppsByResourceGroup(ctx context.Context, sub, rg string) ([]containerapps.ContainerApp, error)
	ListAppsBySubscription(ctx context.Context, sub string) ([]containerapps.ContainerApp, error)

	ListRevisions(ctx context.Context, sub, rg, app string) ([]containerapps.Revision, error)
	GetRevision(ctx context.Context, sub, rg, app, rev string) (containerapps.Revision, error)
	ActivateRevision(ctx context.Context, sub, rg, app, rev string) error
	DeactivateRevision(ctx context.Context, sub, rg, app, rev string) error
	RestartRevision(ctx context.Context, sub, rg, app, rev string) error

	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.App ARM requests for both resource types.
type Handler struct {
	store Store
}

// New returns a Container Apps handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a Microsoft.App managedEnvironments or
// containerApps URL. Provider and type are matched case-insensitively because
// SDK URL templates and hand-written tooling differ in casing.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok || !strings.EqualFold(rp.Provider, providerName) {
		return false
	}

	return strings.EqualFold(rp.ResourceType, typeEnvironments) ||
		strings.EqualFold(rp.ResourceType, typeContainerApps)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if strings.EqualFold(rp.ResourceType, typeEnvironments) {
		h.serveEnvironment(w, r, &rp)
		return
	}

	h.serveApp(w, r, &rp)
}

// PurgeResourceGroup deletes every Container Apps resource under sub/rg so a
// resource-group delete cascades into them (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) serveEnvironment(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listEnvironments(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putEnvironment(w, r, rp)
	case http.MethodGet:
		h.getEnvironment(w, r, rp)
	case http.MethodDelete:
		h.deleteEnvironment(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putEnvironment(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req envRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := containerapps.EnvironmentInput{Location: req.Location, Tags: req.Tags}
	if req.Properties.AppLogsConfiguration != nil {
		in.AppLogs = &containerapps.AppLogsConfiguration{Destination: req.Properties.AppLogsConfiguration.Destination}
	}

	env, created, err := h.store.CreateOrUpdateEnvironment(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createStatus(created), toEnvResponse(&env))
}

func (h *Handler) getEnvironment(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	env, err := h.store.GetEnvironment(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toEnvResponse(&env))
}

func (h *Handler) deleteEnvironment(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteEnvironment(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listEnvironments(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	writeList(w, r, rp,
		h.store.ListEnvironmentsByResourceGroup, h.store.ListEnvironmentsBySubscription, toEnvResponse)
}

func (h *Handler) serveApp(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listApps(w, r, rp)
		return
	}

	if strings.EqualFold(rp.SubResource, subResourceRevisions) {
		h.serveRevision(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		h.putApp(w, r, rp)
	case http.MethodGet:
		h.getApp(w, r, rp)
	case http.MethodDelete:
		h.deleteApp(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putApp(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req appRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := toAppInput(&req)

	app, created, err := h.store.CreateOrUpdateApp(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createStatus(created), toAppResponse(&app))
}

func (h *Handler) getApp(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	app, err := h.store.GetApp(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAppResponse(&app))
}

func (h *Handler) deleteApp(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteApp(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listApps(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	writeList(w, r, rp,
		h.store.ListAppsByResourceGroup, h.store.ListAppsBySubscription, toAppResponse)
}

// serveRevision dispatches the container-app revision sub-resource:
//
//	GET  .../revisions               → list
//	GET  .../revisions/{rev}          → get
//	POST .../revisions/{rev}/{action} → activate | deactivate | restart
func (h *Handler) serveRevision(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		h.listRevisions(w, r, rp)
		return
	}

	if rp.SubResourceAction != "" {
		h.revisionAction(w, r, rp)
		return
	}

	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	rev, err := h.store.GetRevision(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRevisionResponse(rp, &rev))
}

func (h *Handler) listRevisions(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	revs, err := h.store.ListRevisions(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listEnvelope[revisionResponse]{Value: make([]revisionResponse, 0, len(revs))}
	for i := range revs {
		out.Value = append(out.Value, toRevisionResponse(rp, &revs[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// revisionAction runs a POST activate/deactivate/restart on a revision. Each is
// synchronous in armappcontainers and returns an empty body, so a 200 with no
// content terminates the SDK call.
func (h *Handler) revisionAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var err error

	switch strings.ToLower(rp.SubResourceAction) {
	case actionActivate:
		err = h.store.ActivateRevision(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	case actionDeactivate:
		err = h.store.DeactivateRevision(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	case actionRestart:
		err = h.store.RestartRevision(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unknown revision action")
		return
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeList serves a collection GET for either resource type: it dispatches to
// the resource-group or subscription lister based on the parsed path, projects
// each stored record to its wire shape, and writes the {value:[...]} envelope.
// A single generic implementation keeps the environment and container-app list
// paths from duplicating the same dispatch-and-project scaffolding.
func writeList[T, R any](
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	byRG func(context.Context, string, string) ([]T, error),
	bySub func(context.Context, string) ([]T, error),
	project func(*T) R,
) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []T
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = byRG(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = bySub(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listEnvelope[R]{Value: make([]R, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, project(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// createStatus is 201 for a new resource, 200 for an in-place update.
func createStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}

// writeDeleteStatus emits the ARM idempotent-delete status: 200 when a resource
// was deleted, 204 when it was already absent. The armappcontainers BeginDelete
// poller accepts both and terminates immediately.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
