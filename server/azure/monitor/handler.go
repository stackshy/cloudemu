// Package monitor implements the Azure microsoft.insights ARM resources
// (metricAlerts, actionGroups, activityLogAlerts) and the microsoft.insights
// data-plane (metrics, metricDefinitions) plus diagnosticSettings against a
// CloudEmu monitoring driver. Real armmonitor clients hit these handlers the
// same way they hit management.azure.com.
package monitor

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	providerName    = "microsoft.insights"
	typeAlerts      = "metricAlerts"
	typeActionGroup = "actionGroups"
	typeActivityLog = "activityLogAlerts"
	defaultLocation = "global"
)

// Handler serves the microsoft.insights ARM resource types: metricAlerts,
// actionGroups and activityLogAlerts. Each is stored in full so reads round-trip
// the caller's definition; metricAlerts additionally register the named metric
// with the driver so the alarm evaluates.
type Handler struct {
	mon   mondriver.Monitoring
	store *resourceStore
}

// New returns a monitor handler.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{mon: m, store: newResourceStore()}
}

// armType returns the ARM "type" string for a stored resource type.
func armType(resourceType string) string {
	return "Microsoft.Insights/" + resourceType
}

// canonicalType maps an incoming (case-insensitive) resource type to the stored
// kind, or "" when this handler does not serve it.
func canonicalType(resourceType string) string {
	switch strings.ToLower(resourceType) {
	case strings.ToLower(typeAlerts):
		return typeAlerts
	case strings.ToLower(typeActionGroup):
		return typeActionGroup
	case strings.ToLower(typeActivityLog):
		return typeActivityLog
	default:
		return ""
	}
}

// Matches returns true for ARM URLs targeting a microsoft.insights resource type
// this handler serves. The provider name is matched case-insensitively because
// real armmonitor clients emit "Microsoft.Insights" while other tooling emits
// the lowercase form.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) && canonicalType(rp.ResourceType) != ""
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	kind := canonicalType(rp.ResourceType)
	if kind == "" {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown resource type")
		return
	}

	if rp.ResourceName == "" {
		h.list(w, &rp, kind)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp, kind)
	case http.MethodGet:
		h.get(w, &rp, kind)
	case http.MethodDelete:
		h.delete(w, &rp, kind)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req resourceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	res := &armResource{Location: req.Location, Tags: req.Tags, Properties: req.Properties}
	if kind == typeAlerts {
		res.Properties = withProvisioningState(res.Properties)

		if err := h.registerAlarm(r, rp.ResourceName, req.Properties); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	existed := h.store.set(kind, rp.ResourceName, res)

	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, toResourceJSON(rp, kind, res))
}

func (h *Handler) get(w http.ResponseWriter, rp *azurearm.ResourcePath, kind string) {
	res, ok := h.store.get(kind, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", kind+" "+rp.ResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResourceJSON(rp, kind, res))
}

func (h *Handler) list(w http.ResponseWriter, rp *azurearm.ResourcePath, kind string) {
	all := h.store.all(kind)

	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}

	sort.Strings(names)

	out := resourceListResponse{Value: make([]resourceResponse, 0, len(names))}

	for _, name := range names {
		scoped := *rp
		scoped.ResourceName = name
		out.Value = append(out.Value, toResourceJSON(&scoped, kind, all[name]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) delete(w http.ResponseWriter, rp *azurearm.ResourcePath, kind string) {
	if !h.store.delete(kind, rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", kind+" "+rp.ResourceName+" not found")
		return
	}

	if kind == typeAlerts {
		_ = h.mon.DeleteAlarm(context.Background(), rp.ResourceName)
	}

	w.WriteHeader(http.StatusOK)
}
