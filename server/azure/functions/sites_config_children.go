package functions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"

	azfunctions "github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	configNameAuthSettings   = "authsettings"
	configNameAuthSettingsV2 = "authsettingsv2"
	configNamePublishingCred = "publishingcredentials"

	configVersionKey = "configVersion"
)

// configDoc describes one config/{name} document of a site. def is the
// properties object real Azure returns before any write; an empty def means
// the document does not exist until written (backup answers 404). get allows
// GET config/{name}; listMethod is the verb of config/{name}/list, if any.
type configDoc struct {
	def        string
	get        bool
	listMethod string
	validate   func(json.RawMessage) error
}

// lookupConfigDoc returns the stored config document named name (lowercase).
// Shapes follow the Microsoft.Web 2023-12-01 REST reference.
func lookupConfigDoc(name string) (configDoc, bool) {
	switch name {
	case "connectionstrings":
		return configDoc{def: emptyObject, listMethod: http.MethodPost, validate: validateConnectionStrings}, true
	case configNameAuthSettings:
		return configDoc{def: authSettingsDefault, listMethod: http.MethodPost}, true
	case configNameAuthSettingsV2:
		return configDoc{def: authSettingsV2Default, get: true, listMethod: http.MethodGet}, true
	case "logs":
		return configDoc{def: logsDefault, get: true}, true
	case "slotconfignames":
		return configDoc{def: slotConfigNamesDefault, get: true}, true
	case "backup":
		return configDoc{listMethod: http.MethodPost}, true
	case "azurestorageaccounts", "metadata":
		return configDoc{def: emptyObject, listMethod: http.MethodPost}, true
	default:
		return configDoc{}, false
	}
}

const (
	emptyObject            = `{}`
	authSettingsDefault    = `{"enabled":false,"configVersion":"v1"}`
	slotConfigNamesDefault = `{"connectionStringNames":null,"appSettingNames":null,"azureStorageConfigNames":null}`
)

const authSettingsV2Default = `{"platform":{"enabled":false,"runtimeVersion":"~1"},` +
	`"globalValidation":{"requireAuthentication":false,"unauthenticatedClientAction":"RedirectToLoginPage"},` +
	`"identityProviders":{},"login":{"tokenStore":{"enabled":false}},` +
	`"httpSettings":{"requireHttps":true,"routes":{"apiPrefix":"/.auth"},"forwardProxy":{"convention":"NoProxy"}}}`

const logsDefault = `{"applicationLogs":{"fileSystem":{"level":"Off"},"azureBlobStorage":{"level":"Off"}},` +
	`"httpLogs":{"fileSystem":{"retentionInMb":35,"enabled":false},"azureBlobStorage":{"enabled":false}},` +
	`"failedRequestsTracing":{"enabled":false},"detailedErrorMessages":{"enabled":false}}`

// webConfigDefault is the config/web properties a new Linux site reports
// before any site_config is set.
const webConfigDefault = `{"numberOfWorkers":1,"alwaysOn":false,"ftpsState":"FtpsOnly","minTlsVersion":"1.2",` +
	`"scmMinTlsVersion":"1.2","http20Enabled":false,"use32BitWorkerProcess":true,"webSocketsEnabled":false,` +
	`"loadBalancing":"LeastRequests","managedPipelineMode":"Integrated","remoteDebuggingEnabled":false,` +
	`"scmType":"None","vnetRouteAllEnabled":false,"ipSecurityRestrictionsDefaultAction":"Allow",` +
	`"scmIpSecurityRestrictionsDefaultAction":"Allow","scmIpSecurityRestrictionsUseMain":false,` +
	`"ipSecurityRestrictions":[{"ipAddress":"Any","action":"Allow","priority":2147483647,"name":"Allow all"}],` +
	`"scmIpSecurityRestrictions":[{"ipAddress":"Any","action":"Allow","priority":2147483647,"name":"Allow all"}]}`

// errConnectionStringType reports a connection string type outside the
// ConnectionStringType enum.
var errConnectionStringType = errors.New("invalid connection string type")

// isConnectionStringType reports whether t is a ConnectionStringType value.
func isConnectionStringType(t string) bool {
	switch strings.ToLower(t) {
	case "mysql", "sqlserver", "sqlazure", "custom", "notificationhub", "servicebus", "eventhub", "apihub",
		"docdb", "rediscache", "postgresql":
		return true
	default:
		return false
	}
}

// configEnvelope is the ARM body of every config/{name} document.
type configEnvelope struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Location   string          `json:"location,omitempty"`
	Properties json.RawMessage `json:"properties"`
}

// configWriteRequest is a config/{name} PUT or PATCH body.
type configWriteRequest struct {
	Properties json.RawMessage `json:"properties"`
}

// serveConfig dispatches .../sites/{name}/config/{config}[/list]. Config names
// and the list action compare case-insensitively, as ARM does: azurerm sends
// config/appSettings while track-1 SDKs send config/appsettings.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (*Handler) serveConfig(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	name := strings.ToLower(rp.SubResourceName)
	action := strings.ToLower(rp.SubResourceAction)

	if rp.Rest != "" || (action != "" && action != actionList) {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown config route")
		return
	}

	if serveBuiltinConfig(w, r, rp, store, name, action) {
		return
	}

	doc, ok := lookupConfigDoc(name)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType",
			"config "+rp.SubResourceName+" is not a known site configuration")

		return
	}

	serveConfigDoc(w, r, rp, store, name, doc, action)
}

// serveBuiltinConfig serves the config names backed by modeled site fields
// (appsettings, web, publishingcredentials). It reports false for any other.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func serveBuiltinConfig(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps, name, action string,
) bool {
	switch name {
	case configNameAppSettings:
		serveAppSettingsConfig(w, r, rp, store, action)
	case configNameWeb:
		if action != "" {
			writeConfigMethodNotAllowed(w)
			return true
		}

		serveConfigWeb(w, r, rp, store)
	case configNamePublishingCred:
		if action != actionList || r.Method != http.MethodPost {
			writeConfigMethodNotAllowed(w)
			return true
		}

		listPublishingCredentials(w, r, rp, store)
	default:
		return false
	}

	return true
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func serveAppSettingsConfig(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps, action string,
) {
	switch {
	case action == actionList && r.Method == http.MethodPost:
		listAppSettings(w, r, rp, store)
	case action == "" && r.Method == http.MethodPut:
		updateAppSettings(w, r, rp, store)
	default:
		writeConfigMethodNotAllowed(w)
	}
}

func writeConfigMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported config route")
}

// configDocOp maps a request on a config document to the operation it
// performs: PUT (write), DELETE, GET (read, including the list action), or ""
// when the document does not allow it.
func configDocOp(method, action string, doc configDoc) string {
	switch {
	case action == "" && method == http.MethodPut:
		return http.MethodPut
	case action == "" && method == http.MethodDelete && doc.def == "":
		return http.MethodDelete
	case action == "" && method == http.MethodGet && doc.get, action == actionList && method == doc.listMethod:
		return http.MethodGet
	default:
		return ""
	}
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func serveConfigDoc(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
	name string, doc configDoc, action string,
) {
	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	switch configDocOp(r.Method, action, doc) {
	case http.MethodPut:
		putConfigDoc(w, r, rp, store, name, doc)
	case http.MethodDelete:
		deleteConfigDoc(w, r, rp, store, name)
	case http.MethodGet:
		readConfigDoc(w, rp, meta, name, doc)
	default:
		writeConfigMethodNotAllowed(w)
	}
}

// readConfigDoc writes the stored document, its default, or 404 when the
// document has no default and was never written.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func readConfigDoc(w http.ResponseWriter, rp azurearm.ResourcePath, meta *azfunctions.SiteMeta, name string, doc configDoc) {
	props, ok := meta.Configs[name]
	if !ok && doc.def == "" {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "no "+name+" configuration is set for the site")
		return
	}

	if !ok {
		props = json.RawMessage(doc.def)
	}

	azurearm.WriteJSON(w, http.StatusOK, configBody(rp, meta, name, props))
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func deleteConfigDoc(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps, name string) {
	if _, err := store.SetSiteConfigBlob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		name, nil); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func putConfigDoc(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
	name string, doc configDoc,
) {
	props, ok := decodeConfigProps(w, r)
	if !ok {
		return
	}

	if doc.validate != nil {
		if err := doc.validate(props); err != nil {
			azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
	}

	meta, err := store.SetSiteConfigBlob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, name, props)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Writing V2 auth settings switches the site's auth config version, which
	// the v1 document reports and azurerm reads to decide which one applies.
	if name == configNameAuthSettingsV2 {
		meta, err = markAuthSettingsV2(r, rp, store, meta)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, configBody(rp, meta, name, meta.Configs[name]))
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func markAuthSettingsV2(
	r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps, meta *azfunctions.SiteMeta,
) (*azfunctions.SiteMeta, error) {
	v1 := map[string]any{}

	raw, ok := meta.Configs[configNameAuthSettings]
	if !ok {
		raw = json.RawMessage(authSettingsDefault)
	}

	if err := json.Unmarshal(raw, &v1); err != nil {
		v1 = map[string]any{}
	}

	v1[configVersionKey] = "v2"
	out, _ := json.Marshal(v1) //nolint:errchkjson // a decoded JSON object always re-encodes

	return store.SetSiteConfigBlob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		configNameAuthSettings, out)
}

// serveConfigWeb serves config/web: GET returns the stored siteConfig over the
// service defaults, PUT replaces it and PATCH merges into it.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func serveConfigWeb(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodPatch {
		writeConfigMethodNotAllowed(w)
		return
	}

	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if r.Method != http.MethodGet {
		meta, err = writeConfigWeb(w, r, rp, store, meta)
		if meta == nil {
			if err != nil {
				azurearm.WriteCErr(w, err)
			}

			return
		}
	}

	props, _ := json.Marshal(webConfigProps(meta)) //nolint:errchkjson // built from decoded JSON

	azurearm.WriteJSON(w, http.StatusOK, configBody(rp, meta, configNameWeb, props))
}

// writeConfigWeb applies a config/web PUT or PATCH. It returns a nil meta when
// it has already written the response (or err to write).
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func writeConfigWeb(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
	meta *azfunctions.SiteMeta,
) (*azfunctions.SiteMeta, error) {
	props, ok := decodeConfigProps(w, r)
	if !ok {
		return nil, nil
	}

	merged := map[string]any{}
	if r.Method == http.MethodPatch {
		overlayJSON(merged, meta.SiteConfig)
	}

	overlayJSON(merged, props)

	if _, err := store.SetSiteConfigBlob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		configNameWeb, sanitizeSiteConfig(merged)); err != nil {
		return nil, err
	}

	var knobs patchSiteConfig
	if err := json.Unmarshal(props, &knobs); err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return nil, nil
	}

	return store.PatchSiteMeta(r.Context(), rp.Subscription, meta.ResourceGroup, rp.ResourceName,
		azfunctions.SiteMetaPatch{
			LinuxFxVersion: knobs.LinuxFxVersion,
			AlwaysOn:       knobs.AlwaysOn,
			FtpsState:      knobs.FtpsState,
			MinTLSVersion:  knobs.MinTLSVersion,
		})
}

// webConfigProps is the config/web properties: defaults, then the stored
// siteConfig, then the modeled knobs and the app settings.
func webConfigProps(meta *azfunctions.SiteMeta) map[string]any {
	out := map[string]any{}
	overlayJSON(out, json.RawMessage(webConfigDefault))
	overlayJSON(out, meta.SiteConfig)

	if meta.LinuxFxVersion != "" {
		out["linuxFxVersion"] = meta.LinuxFxVersion
	}

	if meta.AlwaysOn != nil {
		out["alwaysOn"] = *meta.AlwaysOn
	}

	if meta.FtpsState != "" {
		out["ftpsState"] = meta.FtpsState
	}

	if meta.MinTLSVersion != "" {
		out["minTlsVersion"] = meta.MinTLSVersion
	}

	// Real ARM returns appSettings null on config/web: the values are read
	// only through config/appsettings/list.
	out["appSettings"] = nil

	return out
}

// sanitizeSiteConfig drops the secret-bearing lists from a stored siteConfig:
// app settings live in SiteMeta.AppSettings and connection strings in their
// own config document, so neither is kept twice.
func sanitizeSiteConfig(cfg map[string]any) json.RawMessage {
	if len(cfg) == 0 {
		return nil
	}

	cfg = maps.Clone(cfg)
	delete(cfg, "appSettings")
	delete(cfg, "connectionStrings")

	out, _ := json.Marshal(cfg) //nolint:errchkjson // built from decoded JSON

	return out
}

// overlayJSON copies the top-level keys of a JSON object onto dst. Anything
// that is not an object is ignored.
func overlayJSON(dst map[string]any, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}

	var src map[string]any
	if json.Unmarshal(raw, &src) != nil {
		return
	}

	maps.Copy(dst, src)
}

// decodeConfigProps reads the properties object of a config write. A missing
// or null object is stored as {}.
func decodeConfigProps(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBytes)

	var req configWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return nil, false
	}

	if len(req.Properties) == 0 || string(req.Properties) == "null" {
		return json.RawMessage(emptyObject), true
	}

	var obj map[string]any
	if err := json.Unmarshal(req.Properties, &obj); err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", "properties must be an object")
		return nil, false
	}

	return req.Properties, true
}

// validateConnectionStrings rejects a connection string whose type is not a
// ConnectionStringType value, as ARM does.
func validateConnectionStrings(raw json.RawMessage) error {
	var conns map[string]struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(raw, &conns); err != nil {
		return err
	}

	for name, c := range conns {
		if !isConnectionStringType(c.Type) {
			return fmt.Errorf("%w '%s' for %s", errConnectionStringType, c.Type, name)
		}
	}

	return nil
}

//nolint:gocritic // rp is request-scoped.
func configBody(rp azurearm.ResourcePath, meta *azfunctions.SiteMeta, name string, props json.RawMessage) configEnvelope {
	return configEnvelope{
		ID:         siteID(rp) + "/config/" + name,
		Name:       name,
		Type:       configResourceType,
		Location:   meta.Location,
		Properties: props,
	}
}

// listPublishingCredentials serves POST config/publishingcredentials/list. The
// password is minted once at site create, so repeated calls agree.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func listPublishingCredentials(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	user := "$" + meta.Name
	props, _ := json.Marshal(map[string]string{ //nolint:errchkjson // string map
		"publishingUserName": user,
		"publishingPassword": meta.PublishingPassword,
		"scmUri":             "https://" + user + ":" + meta.PublishingPassword + "@" + meta.Name + ".scm.azurewebsites.net",
	})

	azurearm.WriteJSON(w, http.StatusOK, configEnvelope{
		ID:         siteID(rp) + "/publishingcredentials/" + user,
		Name:       meta.Name,
		Type:       providerName + "/" + resourceType + "/publishingcredentials",
		Properties: props,
	})
}
