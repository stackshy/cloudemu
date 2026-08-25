package functions

import (
	"encoding/json"
	"io"
	"net/http"

	azfunctions "github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	subResourceConfig    = "config"
	subResourceHost      = "host"
	subResourceFunctions = "functions"
	subResourceRestart   = "restart"

	configNameWeb         = "web"
	configNameAppSettings = "appsettings"
	actionList            = "list"
	hostNameDefault       = "default"
	actionListKeys        = "listkeys"

	configResourceType   = providerName + "/" + resourceType + "/config"
	functionResourceType = providerName + "/" + resourceType + "/functions"
)

// serveSiteSubResource dispatches the Azure-only site sub-routes: config
// (appsettings/web), host keys, deployed functions, and the restart verb.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) serveSiteSubResource(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	store, ok := h.siteStore()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"function-app sub-resources not supported by this backend")

		return
	}

	switch rp.SubResource {
	case subResourceConfig:
		h.serveConfig(w, r, rp, store)
	case subResourceHost:
		h.serveHostKeys(w, r, rp, store)
	case subResourceFunctions:
		h.serveFunctions(w, r, rp, store)
	case subResourceRestart:
		h.serveRestart(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported sub-resource")
	}
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (*Handler) serveConfig(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	switch {
	case rp.SubResourceName == configNameWeb && r.Method == http.MethodGet:
		getConfigWeb(w, r, rp, store)
	case rp.SubResourceName == configNameAppSettings && rp.SubResourceAction == actionList && r.Method == http.MethodPost:
		listAppSettings(w, r, rp, store)
	case rp.SubResourceName == configNameAppSettings && rp.SubResourceAction == "" && r.Method == http.MethodPut:
		updateAppSettings(w, r, rp, store)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported config route")
	}
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func getConfigWeb(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, siteConfigResource{
		ID:   siteID(rp) + "/config/web",
		Name: configNameWeb,
		Type: configResourceType,
		Properties: siteConfig{
			LinuxFxVersion: meta.LinuxFxVersion,
			AppSettings:    appSettingsSlice(meta.AppSettings),
		},
	})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func listAppSettings(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	settings := meta.AppSettings
	if settings == nil {
		settings = map[string]string{}
	}

	azurearm.WriteJSON(w, http.StatusOK, stringDictionary{
		ID:         siteID(rp) + "/config/appsettings",
		Name:       configNameAppSettings,
		Type:       configResourceType,
		Kind:       "app",
		Properties: settings,
	})
}

// updateAppSettings serves PUT .../config/appsettings — WebApps_
// UpdateApplicationSettings. Per the ARM contract, this replaces the app's
// entire settings map (not a merge) and echoes it back in the response.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func updateAppSettings(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBytes)

	var req stringDictionary
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	meta, err := store.UpdateAppSettings(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Properties)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, stringDictionary{
		ID:         siteID(rp) + "/config/appsettings",
		Name:       configNameAppSettings,
		Type:       configResourceType,
		Kind:       "app",
		Properties: nonNilMap(meta.AppSettings),
	})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (*Handler) serveHostKeys(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
) {
	if rp.SubResourceName != hostNameDefault || rp.SubResourceAction != actionListKeys || r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported host route")
		return
	}

	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, hostKeys{
		MasterKey:    meta.MasterKey,
		FunctionKeys: nonNilMap(meta.HostFunctionKeys),
		SystemKeys:   nonNilMap(meta.SystemKeys),
	})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) serveRestart(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "restart requires POST")
		return
	}

	if _, err := h.getFunction(r.Context(), rp); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// siteID builds the parent site's ARM resource id.
//
//nolint:gocritic // rp is request-scoped.
func siteID(rp azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName)
}

// appSettingsSlice renders a settings map as ARM name/value pairs.
func appSettingsSlice(settings map[string]string) []nameValue {
	if len(settings) == 0 {
		return nil
	}

	out := make([]nameValue, 0, len(settings))
	for k, v := range settings {
		out = append(out, nameValue{Name: k, Value: v})
	}

	return out
}

// nonNilMap returns m, or an empty map so the JSON encodes {} rather than null.
func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}

// toFunctionEnvelope renders a deployed function as its ARM FunctionEnvelope.
//
//nolint:gocritic // rp is request-scoped.
func toFunctionEnvelope(rp azurearm.ResourcePath, fn *azfunctions.SiteFunction) functionEnvelope {
	site := rp.ResourceName

	return functionEnvelope{
		ID:   siteID(rp) + "/functions/" + fn.Name,
		Name: fn.Name,
		Type: functionResourceType,
		Properties: functionEnvelopeProps{
			Name:          fn.Name,
			FunctionAppID: siteID(rp),
			Config:        fn.Config,
			Href:          "https://" + site + ".azurewebsites.net/admin/functions/" + fn.Name,
			Language:      fn.Language,
			IsDisabled:    fn.IsDisabled,
		},
	}
}
