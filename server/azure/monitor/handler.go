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
	typeAutoscale   = "autoscaleSettings"
	defaultLocation = "global"
)

// actionGroupRegistrar is the optional driver capability that lets the handler
// wire an action group's receivers into the alarm-evaluation engine, so a metric
// alert that names the group delivers to its receivers on a breach. The Azure
// Monitor mock implements it; a driver that does not simply skips the wiring.
type actionGroupRegistrar interface {
	RegisterActionGroup(id string, properties map[string]any)
	UnregisterActionGroup(id string)
}

// Handler serves the microsoft.insights ARM resource types: metricAlerts,
// actionGroups, activityLogAlerts and autoscaleSettings. Each is stored in full
// so reads round-trip the caller's definition; metricAlerts additionally
// register the named metric with the driver so the alarm evaluates, and
// actionGroups register their receivers so a breach delivers to them.
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
	case strings.ToLower(typeAutoscale):
		return typeAutoscale
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
	case http.MethodPatch:
		h.patch(w, r, &rp, kind)
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
	}

	if err := h.applySideEffects(r, rp, kind, res.Properties); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	existed := h.store.set(rp.Subscription, rp.ResourceGroup, kind, rp.ResourceName, res)

	// Metric Alerts - Create Or Update documents a single response: 200 OK,
	// for both a first create and a subsequent update (unlike actionGroups and
	// activityLogAlerts, which the REST reference documents as 201 Created on
	// first create / 200 OK on update).
	status := http.StatusOK
	if kind != typeAlerts && !existed {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResourceJSON(rp, kind, res))
}

func (h *Handler) get(w http.ResponseWriter, rp *azurearm.ResourcePath, kind string) {
	res, ok := h.store.get(rp.Subscription, rp.ResourceGroup, kind, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", kind+" "+rp.ResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResourceJSON(rp, kind, res))
}

func (h *Handler) list(w http.ResponseWriter, rp *azurearm.ResourcePath, kind string) {
	all := h.store.all(rp.Subscription, rp.ResourceGroup, kind)

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
	// Azure DELETE is idempotent: a missing metricAlert/actionGroup/activityLogAlert
	// returns 204 No Content ("resource does not exist"), not a 404 error body.
	if !h.store.delete(rp.Subscription, rp.ResourceGroup, kind, rp.ResourceName) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if kind == typeAlerts {
		_ = h.mon.DeleteAlarm(context.Background(), rp.ResourceName)
	}

	if kind == typeActionGroup {
		h.unregisterActionGroup(rp)
	}

	w.WriteHeader(http.StatusOK)
}

// patch applies an ARM Update (HTTP PATCH) over a stored resource under nil-mask
// semantics: omitted top-level fields (location, tags, properties) are
// preserved, supplied ones are merged over the stored value. metricAlerts and
// actionGroups re-run their side-effect wiring against the merged properties so
// an updated threshold or receiver set takes effect. Update on a missing
// resource is a 404, matching the real ARM Update contract.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind string) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	existing, ok := h.store.get(rp.Subscription, rp.ResourceGroup, kind, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", kind+" "+rp.ResourceName+" not found")
		return
	}

	var req resourceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	merged := mergeResource(existing, &req)
	if kind == typeAlerts {
		merged.Properties = withProvisioningState(merged.Properties)
	}

	if err := h.applySideEffects(r, rp, kind, merged.Properties); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.store.set(rp.Subscription, rp.ResourceGroup, kind, rp.ResourceName, merged)

	azurearm.WriteJSON(w, http.StatusOK, toResourceJSON(rp, kind, merged))
}

// applySideEffects runs the driver-side wiring a resource's stored properties
// imply: metricAlerts register/refresh the evaluated alarm, actionGroups
// register/refresh their receivers. Other kinds (activityLogAlerts,
// autoscaleSettings) are pure round-trip resources with no side effect.
func (h *Handler) applySideEffects(r *http.Request, rp *azurearm.ResourcePath, kind string, props map[string]any) error {
	switch kind {
	case typeAlerts:
		return h.registerAlarm(r, rp.ResourceName, props)
	case typeActionGroup:
		h.registerActionGroup(rp, props)
	}

	return nil
}

// registerActionGroup wires an action group's receivers into the alarm engine
// under its ARM resource id when the driver supports it.
func (h *Handler) registerActionGroup(rp *azurearm.ResourcePath, props map[string]any) {
	reg, ok := h.mon.(actionGroupRegistrar)
	if !ok {
		return
	}

	reg.RegisterActionGroup(actionGroupID(rp), props)
}

// unregisterActionGroup removes an action group's receiver wiring on delete.
func (h *Handler) unregisterActionGroup(rp *azurearm.ResourcePath) {
	reg, ok := h.mon.(actionGroupRegistrar)
	if !ok {
		return
	}

	reg.UnregisterActionGroup(actionGroupID(rp))
}

// actionGroupID builds the ARM resource id a metric alert references an action
// group by (properties.actions[].actionGroupId).
func actionGroupID(rp *azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeActionGroup, rp.ResourceName)
}
