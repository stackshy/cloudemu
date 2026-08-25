// Package functions serves Azure ARM Microsoft.Web/sites (Function Apps)
// requests against a CloudEmu serverless driver. Real azure-sdk-for-go
// armappservice clients configured with a custom endpoint hit this handler
// the same way they hit management.azure.com.
//
// MVP coverage:
//
//	PUT    .../sites/{name}        — CreateOrUpdate
//	GET    .../sites/{name}        — Get
//	GET    .../sites               — List in resource group / subscription
//	DELETE .../sites/{name}        — Delete
//	PUT    .../serverfarms/{name}  — CreateOrUpdate App Service plan
//	GET    .../serverfarms/{name}  — Get App Service plan
//	POST   /api/{name}             — Synchronous invoke (non-ARM, mirrors how
//	                               real Function Apps are hit at
//	                               <app>.azurewebsites.net/api/<name>)
//
// Versions, slots, deployment, scaling, and Kudu/SCM endpoints are deferred.
package functions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	azfunctions "github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

const (
	providerName    = "Microsoft.Web"
	resourceType    = "sites"
	serverFarmsType = "serverfarms"

	functionAppKind  = "functionapp"
	defaultLocation  = "eastus"
	invokePathPrefix = "/api/"
	maxInvokeBytes   = 1 << 20 // 1 MiB
	maxControlBytes  = 1 << 20

	// extensionsSubResource + zipDeployName model the Kudu zipdeploy route
	// PUT .../sites/{name}/extensions/zipdeploy, whose body is the raw zip.
	extensionsSubResource = "extensions"
	zipDeployName         = "zipdeploy"
	maxDeployBytes        = 50 << 20 // 50 MiB deployment package

	// handlerAppSettingKey is a reserved app setting that names the function's
	// handler entrypoint (e.g. "function_app.main"). Azure has no static handler
	// coordinate on the site resource, so it is carried here, read into
	// FunctionConfig.Handler, and stripped so it isn't exposed as a literal env
	// var to the running code.
	handlerAppSettingKey = "_CLOUDEMU_HANDLER"
)

// appServicePlanStore is the App Service plan surface the handler needs on top
// of the serverless driver. The Azure provider Mock (*azfunctions.Mock)
// satisfies it; backends that don't model plans fall through to 501.
type appServicePlanStore interface {
	CreateAppServicePlan(ctx context.Context, p azfunctions.AppServicePlan) (*azfunctions.AppServicePlan, error)
	GetAppServicePlan(ctx context.Context, subscription, resourceGroup, name string) (*azfunctions.AppServicePlan, error)
	DeleteAppServicePlan(ctx context.Context, subscription, resourceGroup, name string) error
	ListAppServicePlans(ctx context.Context, subscription, resourceGroup string) ([]azfunctions.AppServicePlan, error)
}

// azureFunctionApps is the Azure-only site surface the handler layers on top of
// the portable serverless driver: region/plan metadata, app settings, host and
// function keys, and deployed functions. Only the Azure provider Mock
// (*azfunctions.Mock) satisfies it; other backends fall through to the generic
// portable behavior (or 501 for Azure-only sub-routes).
type azureFunctionApps interface {
	UpsertSiteMeta(ctx context.Context, in azfunctions.SiteMeta) (*azfunctions.SiteMeta, error)
	GetSiteMeta(ctx context.Context, subscription, resourceGroup, name string) (*azfunctions.SiteMeta, error)
	DeleteSiteMeta(ctx context.Context, subscription, resourceGroup, name string) error
	ListSiteMeta(ctx context.Context, subscription, resourceGroup string) ([]azfunctions.SiteMeta, error)
	CreateSiteFunction(ctx context.Context, site string, fn azfunctions.SiteFunction) (*azfunctions.SiteFunction, error)
	GetSiteFunction(ctx context.Context, site, name string) (*azfunctions.SiteFunction, error)
	ListSiteFunctions(ctx context.Context, site string) ([]azfunctions.SiteFunction, error)
	DeleteSiteFunction(ctx context.Context, site, name string) error
	FunctionKeys(ctx context.Context, site, name string) (map[string]string, error)
	UpdateAppSettings(
		ctx context.Context, subscription, resourceGroup, name string, settings map[string]string,
	) (*azfunctions.SiteMeta, error)
}

// azureScopedSites optionally scopes a site's get/delete to the (subscription,
// resourceGroup) it was created under. The underlying portable function record
// (sdrv.Serverless) is keyed by name alone across every resource group —
// matching real Azure's globally-unique Web App names — so without this, an
// ARM GET/DELETE against the wrong resourceGroups segment would return or
// remove another resource group's site. Only the Azure provider Mock
// (*azfunctions.Mock) satisfies it; other backends fall back to the unscoped
// sdrv.Serverless calls.
type azureScopedSites interface {
	GetFunctionScoped(ctx context.Context, subscription, resourceGroup, name string) (*sdrv.FunctionInfo, error)
	DeleteFunctionScoped(ctx context.Context, subscription, resourceGroup, name string) error
}

// Handler serves ARM JSON requests for Microsoft.Web/sites and direct invoke
// requests at /api/{name}.
type Handler struct {
	fn sdrv.Serverless
}

// New returns a Functions handler backed by fn.
func New(fn sdrv.Serverless) *Handler {
	return &Handler{fn: fn}
}

// siteStore returns the Azure site surface when the backend provides it.
func (h *Handler) siteStore() (azureFunctionApps, bool) {
	s, ok := h.fn.(azureFunctionApps)

	return s, ok
}

// Matches accepts ARM Microsoft.Web/sites paths plus the non-ARM /api/{name}
// invoke shape.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, invokePathPrefix) {
		return true
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	return rp.ResourceType == resourceType || strings.EqualFold(rp.ResourceType, serverFarmsType)
}

// ServeHTTP routes requests by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, invokePathPrefix) {
		h.serveInvoke(w, r)
		return
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if strings.EqualFold(rp.ResourceType, serverFarmsType) {
		h.servePlan(w, r, rp)
		return
	}

	switch {
	case rp.ResourceName != "":
		h.serveResource(w, r, rp)
	default:
		h.serveCollection(w, r, rp)
	}
}

//nolint:gocritic // rp is a request-scoped value; copying is cheap.
func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	// A code deploy (Kudu zipdeploy) is modeled as a sub-resource PUT on the
	// site: .../sites/{name}/extensions/zipdeploy carrying the raw zip bytes.
	// Handle it before createOrUpdate so the sub-resource path doesn't get
	// misrouted as a site create/update.
	if strings.EqualFold(rp.SubResource, extensionsSubResource) &&
		strings.EqualFold(rp.SubResourceName, zipDeployName) {
		if r.Method == http.MethodPut {
			h.zipDeploy(w, r, rp)
		} else {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "zipdeploy requires PUT")
		}

		return
	}

	// config/appsettings/list, config/web, host/default/listkeys, functions[/...],
	// and the restart verb are Azure-only site sub-routes.
	if rp.SubResource != "" {
		h.serveSiteSubResource(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, rp)
	case http.MethodGet:
		h.get(w, r, rp)
	case http.MethodDelete:
		h.delete(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value; copying is cheap.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.list(w, r, rp)
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxControlBytes)

	var req createSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	// Pull the handler entrypoint out of the reserved app setting and drop it
	// from the settings the function sees, so it never leaks as a literal env
	// var (and isn't echoed back in the response).
	handler, settings := extractHandlerSetting(req.Properties.SiteConfig.AppSettings)

	cfg := sdrv.FunctionConfig{
		Name:        rp.ResourceName,
		Runtime:     req.Properties.SiteConfig.LinuxFxVersion,
		Handler:     handler,
		Tags:        req.Tags,
		Environment: appSettingsToMap(settings),
	}

	info, err := upsertFunction(r, h.fn, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	meta := h.upsertSiteMeta(r, rp, req, settings)

	azurearm.WriteJSON(w, http.StatusOK, toSiteResource(rp, info, meta))
}

// upsertSiteMeta records the Azure-only site metadata (region, plan flags, app
// settings) when the backend supports it, returning the stored view used to
// build the response. Returns nil for backends without the site surface.
//
//nolint:gocritic // rp/req travel the dispatch chain once per request.
func (h *Handler) upsertSiteMeta(
	r *http.Request, rp azurearm.ResourcePath, req createSiteRequest, settings []nameValue,
) *azfunctions.SiteMeta {
	store, ok := h.siteStore()
	if !ok {
		return nil
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	meta, err := store.UpsertSiteMeta(r.Context(), azfunctions.SiteMeta{
		Name:           rp.ResourceName,
		Subscription:   rp.Subscription,
		ResourceGroup:  rp.ResourceGroup,
		Location:       location,
		ServerFarmID:   req.Properties.ServerFarmID,
		HTTPSOnly:      req.Properties.HTTPSOnly,
		Reserved:       req.Properties.Reserved,
		LinuxFxVersion: req.Properties.SiteConfig.LinuxFxVersion,
		AppSettings:    appSettingsToMap(settings),
	})
	if err != nil {
		return nil
	}

	return meta
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := h.getFunction(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSiteResource(rp, info, h.siteMeta(r, rp)))
}

// getFunction resolves the function for rp, scoped to its (subscription,
// resourceGroup) when the backend supports it (see azureScopedSites) so a
// site created in one resource group is never returned through another.
// Backends without site-scope support fall back to the unscoped lookup.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) getFunction(ctx context.Context, rp azurearm.ResourcePath) (*sdrv.FunctionInfo, error) {
	if scoped, ok := h.fn.(azureScopedSites); ok {
		return scoped.GetFunctionScoped(ctx, rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	}

	return h.fn.GetFunction(ctx, rp.ResourceName)
}

// siteMeta fetches the stored Azure metadata for a site scoped to rp's
// (subscription, resourceGroup), or nil when absent or out of scope.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) siteMeta(r *http.Request, rp azurearm.ResourcePath) *azfunctions.SiteMeta {
	store, ok := h.siteStore()
	if !ok {
		return nil
	}

	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		return nil
	}

	return meta
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if store, ok := h.siteStore(); ok {
		h.listFromMeta(w, r, rp, store)

		return
	}

	infos, err := h.fn.ListFunctions(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]siteResource, 0, len(infos))

	for i := range infos {
		scope := rp
		scope.ResourceName = infos[i].Name
		out = append(out, toSiteResource(scope, &infos[i], nil))
	}

	azurearm.WriteJSON(w, http.StatusOK, siteListResponse{Value: out})
}

// listFromMeta lists sites filtered by the request scope (resource group, or the
// whole subscription for a sub-wide list), building each id from the site's true
// scope so a sub-wide list never emits an empty resourceGroups segment.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) listFromMeta(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
) {
	metas, err := store.ListSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]siteResource, 0, len(metas))

	for i := range metas {
		info, gerr := h.fn.GetFunction(r.Context(), metas[i].Name)
		if gerr != nil {
			continue
		}

		scope := azurearm.ResourcePath{
			Subscription:  metas[i].Subscription,
			ResourceGroup: metas[i].ResourceGroup,
			ResourceName:  metas[i].Name,
		}
		out = append(out, toSiteResource(scope, info, &metas[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, siteListResponse{Value: out})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if scoped, ok := h.fn.(azureScopedSites); ok {
		// DeleteFunctionScoped atomically checks the site's (subscription,
		// resourceGroup) and removes both the function and its site metadata,
		// so a DELETE against the wrong resourceGroups segment 404s instead
		// of deleting another resource group's site.
		if err := scoped.DeleteFunctionScoped(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)

		return
	}

	if err := h.fn.DeleteFunction(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if store, ok := h.siteStore(); ok {
		_ = store.DeleteSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	}

	w.WriteHeader(http.StatusOK)
}

// zipDeploy handles PUT .../sites/{name}/extensions/zipdeploy, a deliberate
// emulation of Kudu zipdeploy layered onto ARM: the raw request body is the
// deployment zip, which is deployed to the function's code (and, when a
// FunctionEngine is configured, made runnable). The site must already exist
// (created by an ARM site PUT), matching real Azure where zipdeploy targets a
// provisioned Function App.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) zipDeploy(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDeployBytes)

	code, err := io.ReadAll(r.Body)
	if err != nil {
		azurearm.WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", err.Error())
		return
	}

	if len(code) == 0 {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", "empty deployment package")
		return
	}

	_, uerr := h.fn.UpdateFunction(r.Context(), rp.ResourceName, sdrv.FunctionConfig{
		Name: rp.ResourceName,
		Code: code,
	})
	if uerr != nil {
		azurearm.WriteCErr(w, uerr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// servePlanSitesSubResource is the SubResource value of
// GET .../serverfarms/{name}/sites — Plans.ListWebApps.
const servePlanSitesSubResource = "sites"

// servePlan routes Microsoft.Web/serverfarms (App Service plan) requests. Only
// backends that model plans (the Azure provider Mock) are served; others 501.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) servePlan(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	store, ok := h.fn.(appServicePlanStore)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"app service plans not supported by this backend")
		return
	}

	if rp.ResourceName == "" {
		if r.Method == http.MethodGet {
			listPlans(w, r, rp, store)
		} else {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

		return
	}

	if strings.EqualFold(rp.SubResource, servePlanSitesSubResource) {
		if r.Method == http.MethodGet {
			h.listPlanWebApps(w, r, rp, store)
		} else {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodPut:
		createPlan(w, r, rp, store)
	case http.MethodGet:
		getPlan(w, r, rp, store)
	case http.MethodDelete:
		deletePlan(w, r, rp, store)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// listPlans serves the serverfarms collection GET — Plans.List /
// ListByResourceGroup. rp.ResourceGroup empty means the whole subscription;
// each row's scope is built from the plan's own stored resource group so a
// subscription-wide list never emits an empty resourceGroups segment.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func listPlans(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store appServicePlanStore) {
	plans, err := store.ListAppServicePlans(r.Context(), rp.Subscription, rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]serverFarmResource, 0, len(plans))

	for i := range plans {
		scope := azurearm.ResourcePath{
			Subscription: rp.Subscription, ResourceGroup: plans[i].ResourceGroup, ResourceName: plans[i].Name,
		}
		out = append(out, toServerFarmResource(scope, &plans[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, serverFarmListResponse{Value: out})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func createPlan(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store appServicePlanStore) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxControlBytes)

	var req createServerFarmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	plan, err := store.CreateAppServicePlan(r.Context(), azfunctions.AppServicePlan{
		Name:          rp.ResourceName,
		Subscription:  rp.Subscription,
		ResourceGroup: rp.ResourceGroup,
		Location:      req.Location,
		SKUName:       req.SKU.Name,
		SKUTier:       req.SKU.Tier,
		Kind:          req.Kind,
		Capacity:      req.SKU.Capacity,
		Tags:          req.Tags,
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toServerFarmResource(rp, plan))
}

// deletePlan serves DELETE .../serverfarms/{name} — Plans.Delete. Real Azure
// answers 200 or 204 on success; this mock always answers 200, matching the
// site DELETE above.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func deletePlan(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store appServicePlanStore) {
	if err := store.DeleteAppServicePlan(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listPlanWebApps serves GET .../serverfarms/{name}/sites — Plans.ListWebApps.
// It joins on the site's stored ServerFarmID (set at site create/update time
// from properties.serverFarmId) rather than keeping a reverse index on the
// plan, so the plan and its apps can never drift out of sync.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) listPlanWebApps(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store appServicePlanStore) {
	if _, err := store.GetAppServicePlan(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	siteStore, ok := h.siteStore()
	if !ok {
		azurearm.WriteJSON(w, http.StatusOK, siteListResponse{Value: []siteResource{}})
		return
	}

	planID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, serverFarmsType, rp.ResourceName)

	// A site's plan can live in a different resource group than the site
	// itself, so every site in the subscription is a candidate — not just
	// this resource group's.
	metas, err := siteStore.ListSiteMeta(r.Context(), rp.Subscription, "")
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]siteResource, 0, len(metas))

	for i := range metas {
		if !strings.EqualFold(metas[i].ServerFarmID, planID) {
			continue
		}

		info, gerr := h.fn.GetFunction(r.Context(), metas[i].Name)
		if gerr != nil {
			continue
		}

		scope := azurearm.ResourcePath{
			Subscription: metas[i].Subscription, ResourceGroup: metas[i].ResourceGroup, ResourceName: metas[i].Name,
		}
		out = append(out, toSiteResource(scope, info, &metas[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, siteListResponse{Value: out})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func getPlan(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store appServicePlanStore) {
	plan, err := store.GetAppServicePlan(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toServerFarmResource(rp, plan))
}

//nolint:gocritic // rp is request-scoped.
func toServerFarmResource(rp azurearm.ResourcePath, plan *azfunctions.AppServicePlan) serverFarmResource {
	location := plan.Location
	if location == "" {
		location = defaultLocation
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup,
		providerName, serverFarmsType, rp.ResourceName)

	return serverFarmResource{
		ID:       id,
		Name:     plan.Name,
		Type:     providerName + "/" + serverFarmsType,
		Kind:     plan.Kind,
		Location: location,
		Tags:     plan.Tags,
		SKU: &serverFarmSKU{
			Name:     plan.SKUName,
			Tier:     plan.SKUTier,
			Capacity: plan.Capacity,
		},
		Properties: serverFarmProperties{
			ProvisioningState: "Succeeded",
			Status:            "Ready",
		},
	}
}

func (h *Handler) serveInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "invoke requires POST")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, invokePathPrefix)
	if name == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing function name")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxInvokeBytes)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		azurearm.WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", err.Error())
		return
	}

	out, ierr := h.fn.Invoke(r.Context(), sdrv.InvokeInput{
		FunctionName: name,
		Payload:      payload,
		InvokeType:   "RequestResponse",
	})
	if ierr != nil {
		azurearm.WriteCErr(w, ierr)
		return
	}

	if out.Error != "" {
		// Real Azure Functions return 500 + plain-text error body when the
		// handler throws.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(out.Error))

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Payload)
}

// upsertFunction creates the function on first call and updates it on subsequent
// calls — ARM PUT is idempotent and SDKs use it for both.
//
//nolint:gocritic // cfg is the canonical request payload; copying once per PUT is fine.
func upsertFunction(r *http.Request, fn sdrv.Serverless, cfg sdrv.FunctionConfig) (*sdrv.FunctionInfo, error) {
	info, err := fn.CreateFunction(r.Context(), cfg)
	if err == nil {
		return info, nil
	}

	if !cerrors.IsAlreadyExists(err) {
		return nil, err
	}

	return fn.UpdateFunction(r.Context(), cfg.Name, cfg)
}

// toSiteResource renders the ARM site resource. meta carries the Azure-only
// fields (region, provisioning state, plan flags); it is nil for backends
// without the site surface, in which case the region defaults and the plan flags
// are zero. App-setting values are never echoed here — real Azure returns them
// only via the config/appsettings/list POST — so a plain GET does not leak
// secrets.
//
//nolint:gocritic // rp is request-scoped.
func toSiteResource(rp azurearm.ResourcePath, info *sdrv.FunctionInfo, meta *azfunctions.SiteMeta) siteResource {
	location := defaultLocation
	provisioningState := "Succeeded"

	var serverFarmID string

	var httpsOnly, reserved bool

	if meta != nil {
		location = meta.Location
		provisioningState = meta.ProvisioningState
		serverFarmID = meta.ServerFarmID
		httpsOnly = meta.HTTPSOnly
		reserved = meta.Reserved
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup,
		providerName, resourceType, rp.ResourceName)

	hostName := info.Name + ".azurewebsites.net"

	return siteResource{
		ID:       id,
		Name:     info.Name,
		Type:     providerName + "/" + resourceType,
		Kind:     functionAppKind,
		Location: location,
		Tags:     info.Tags,
		Properties: siteProperties{
			State:             "Running",
			ProvisioningState: provisioningState,
			HostNames:         []string{hostName},
			DefaultHostName:   hostName,
			// AppSettings is deliberately emitted (as null) so the server's
			// unmodeled-property echo treats it as owned and never reflects the
			// request's app settings — including secret values — back onto a plain
			// GET. The values are read only via config/appsettings/list.
			SiteConfig: siteConfig{
				LinuxFxVersion: info.Runtime,
			},
			ServerFarmID:        serverFarmID,
			HTTPSOnly:           httpsOnly,
			Reserved:            reserved,
			LastModifiedTimeUtc: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// extractHandlerSetting pulls the reserved handler entrypoint out of the app
// settings and returns it along with the remaining settings (the reserved key
// removed). The handler is empty when the setting is absent.
func extractHandlerSetting(settings []nameValue) (handler string, remaining []nameValue) {
	remaining = make([]nameValue, 0, len(settings))

	for _, kv := range settings {
		if kv.Name == handlerAppSettingKey {
			handler = kv.Value
			continue
		}

		remaining = append(remaining, kv)
	}

	if len(remaining) == 0 {
		remaining = nil
	}

	return handler, remaining
}

func appSettingsToMap(settings []nameValue) map[string]string {
	if len(settings) == 0 {
		return nil
	}

	out := make(map[string]string, len(settings))
	for _, kv := range settings {
		out[kv.Name] = kv.Value
	}

	return out
}
