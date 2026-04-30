// Package functions serves Azure ARM Microsoft.Web/sites (Function Apps)
// requests against a CloudEmu serverless driver. Real azure-sdk-for-go
// armappservice clients configured with a custom endpoint hit this handler
// the same way they hit management.azure.com.
//
// MVP coverage:
//
//	PUT    .../sites/{name}      — CreateOrUpdate
//	GET    .../sites/{name}      — Get
//	GET    .../sites             — List in resource group / subscription
//	DELETE .../sites/{name}      — Delete
//	POST   /api/{name}           — Synchronous invoke (non-ARM, mirrors how
//	                               real Function Apps are hit at
//	                               <app>.azurewebsites.net/api/<name>)
//
// Versions, slots, deployment, scaling, and Kudu/SCM endpoints are deferred.
package functions

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
)

const (
	providerName = "Microsoft.Web"
	resourceType = "sites"

	functionAppKind  = "functionapp"
	defaultLocation  = "eastus"
	invokePathPrefix = "/api/"
	maxInvokeBytes   = 1 << 20 // 1 MiB
	maxControlBytes  = 1 << 20
)

// Handler serves ARM JSON requests for Microsoft.Web/sites and direct invoke
// requests at /api/{name}.
type Handler struct {
	fn sdrv.Serverless
}

// New returns a Functions handler backed by fn.
func New(fn sdrv.Serverless) *Handler {
	return &Handler{fn: fn}
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

	return rp.Provider == providerName && rp.ResourceType == resourceType
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

	switch {
	case rp.ResourceName != "":
		h.serveResource(w, r, rp)
	default:
		h.serveCollection(w, r, rp)
	}
}

//nolint:gocritic // rp is a request-scoped value; copying is cheap.
func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
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

	cfg := sdrv.FunctionConfig{
		Name:        rp.ResourceName,
		Runtime:     req.Properties.SiteConfig.LinuxFxVersion,
		Tags:        req.Tags,
		Environment: appSettingsToMap(req.Properties.SiteConfig.AppSettings),
	}

	info, err := upsertFunction(r, h.fn, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSiteResource(rp, info, req))
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := h.fn.GetFunction(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSiteResource(rp, info, createSiteRequest{}))
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	infos, err := h.fn.ListFunctions(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]siteResource, 0, len(infos))

	for i := range infos {
		scope := rp
		scope.ResourceName = infos[i].Name
		out = append(out, toSiteResource(scope, &infos[i], createSiteRequest{}))
	}

	azurearm.WriteJSON(w, http.StatusOK, siteListResponse{Value: out})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if err := h.fn.DeleteFunction(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
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

//nolint:gocritic // rp is request-scoped.
func toSiteResource(rp azurearm.ResourcePath, info *sdrv.FunctionInfo, req createSiteRequest) siteResource {
	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup,
		providerName, resourceType, rp.ResourceName)

	hostName := info.Name + ".azurewebsites.net"

	settings := req.Properties.SiteConfig.AppSettings
	if len(settings) == 0 {
		settings = mapToAppSettings(info.Environment)
	}

	return siteResource{
		ID:       id,
		Name:     info.Name,
		Type:     providerName + "/" + resourceType,
		Kind:     functionAppKind,
		Location: location,
		Tags:     info.Tags,
		Properties: siteProperties{
			State:           "Running",
			HostNames:       []string{hostName},
			DefaultHostName: hostName,
			SiteConfig: siteConfig{
				LinuxFxVersion: info.Runtime,
				AppSettings:    settings,
			},
			ServerFarmID:        req.Properties.ServerFarmID,
			HTTPSOnly:           req.Properties.HTTPSOnly,
			Reserved:            req.Properties.Reserved,
			LastModifiedTimeUtc: time.Now().UTC().Format(time.RFC3339),
		},
	}
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

func mapToAppSettings(env map[string]string) []nameValue {
	if len(env) == 0 {
		return nil
	}

	out := make([]nameValue, 0, len(env))
	for k, v := range env {
		out = append(out, nameValue{Name: k, Value: v})
	}

	return out
}
