package cache

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	subPatchSchedules = "patchSchedules"
	subFirewallRules  = "firewallRules"

	// patchScheduleName is the only patch schedule name real Azure accepts.
	patchScheduleName = "default"

	typePatchSchedules = "Microsoft.Cache/Redis/PatchSchedules"
	typeFirewallRules  = "Microsoft.Cache/Redis/firewallRules"

	// childMaxDepth is the deepest child route: redis/{c}/{child}/{name}.
	childMaxDepth = 3
)

type scheduleEntryJSON struct {
	DayOfWeek         string `json:"dayOfWeek"`
	StartHourUTC      *int   `json:"startHourUtc"`
	MaintenanceWindow string `json:"maintenanceWindow,omitempty"`
}

type patchScheduleJSON struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	Location   string `json:"location,omitempty"`
	Properties *struct {
		ScheduleEntries []scheduleEntryJSON `json:"scheduleEntries"`
	} `json:"properties"`
}

// childList is the ARM list envelope of a cache child collection.
type childList[T any] struct {
	Value []T `json:"value"`
}

type firewallRuleJSON struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	Properties *struct {
		StartIP string `json:"startIP"`
		EndIP   string `json:"endIP"`
	} `json:"properties"`
}

// serveSubResource routes the children and POST actions of a named cache.
func (h *Handler) serveSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch rp.SubResource {
	case subPatchSchedules, subFirewallRules:
		h.serveChild(w, r, rp)
	default:
		h.serveAction(w, r, rp)
	}
}

// serveChild serves patchSchedules/default and firewallRules/{r}, plus their
// collection lists. Both are stored with the cache and removed with it.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, childMaxDepth) {
		return
	}

	children, ok := h.cache.(cachedriver.RedisChildren)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", rp.SubResource+" not supported by this backend")
		return
	}

	info, ok := h.parentCache(w, r, rp)
	if !ok {
		return
	}

	if rp.SubResource == subPatchSchedules {
		servePatchSchedule(w, r, rp, children, info)
		return
	}

	serveFirewallRule(w, r, rp, children)
}

// parentCache returns the cache the child path names, writing a 404 when it
// is missing or lives in another resource group.
func (h *Handler) parentCache(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) (*cachedriver.CacheInfo, bool) {
	info, err := h.cache.GetCache(r.Context(), rp.ResourceName)
	if err == nil && info.Scope.Matches(scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}) {
		return info, true
	}

	if err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return nil, false
	}

	azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound",
		"Can not perform requested operation on nested resource. Parent resource '"+rp.ResourceName+"' not found.")

	return nil, false
}

func servePatchSchedule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	children cachedriver.RedisChildren, info *cachedriver.CacheInfo,
) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		out := []patchScheduleJSON{}
		if entries, err := children.GetPatchSchedule(r.Context(), rp.ResourceName); err == nil {
			out = append(out, toPatchScheduleJSON(rp, info, entries))
		}

		azurearm.WriteJSON(w, http.StatusOK, childList[patchScheduleJSON]{Value: out})

		return
	}

	if !strings.EqualFold(rp.SubResourceName, patchScheduleName) {
		writeChildNotFound(w, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		putPatchSchedule(w, r, rp, children, info)
	case http.MethodGet:
		entries, err := children.GetPatchSchedule(r.Context(), rp.ResourceName)
		if err != nil {
			writeChildErr(w, rp, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toPatchScheduleJSON(rp, info, entries))
	case http.MethodDelete:
		existed, err := children.DeletePatchSchedule(r.Context(), rp.ResourceName)
		writeChildDelete(w, existed, err)
	default:
		writeMethodNotAllowed(w)
	}
}

func putPatchSchedule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	children cachedriver.RedisChildren, info *cachedriver.CacheInfo,
) {
	var body patchScheduleJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if body.Properties == nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestBody", "properties.scheduleEntries is required")
		return
	}

	entries := make([]cachedriver.PatchScheduleEntry, 0, len(body.Properties.ScheduleEntries))

	for _, e := range body.Properties.ScheduleEntries {
		if e.StartHourUTC == nil {
			azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestBody", "startHourUtc is required")
			return
		}

		entries = append(entries, cachedriver.PatchScheduleEntry{
			DayOfWeek: e.DayOfWeek, StartHourUTC: *e.StartHourUTC, MaintenanceWindow: e.MaintenanceWindow,
		})
	}

	created, err := children.SetPatchSchedule(r.Context(), rp.ResourceName, entries)
	if err != nil {
		writeChildErr(w, rp, err)
		return
	}

	stored, err := children.GetPatchSchedule(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), toPatchScheduleJSON(rp, info, stored))
}

func serveFirewallRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, children cachedriver.RedisChildren,
) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		rules, err := children.ListFirewallRules(r.Context(), rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		out := make([]firewallRuleJSON, 0, len(rules))
		for i := range rules {
			out = append(out, toFirewallRuleJSON(rp, &rules[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, childList[firewallRuleJSON]{Value: out})

		return
	}

	switch r.Method {
	case http.MethodPut:
		putFirewallRule(w, r, rp, children)
	case http.MethodGet:
		rule, err := children.GetFirewallRule(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			writeChildErr(w, rp, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toFirewallRuleJSON(rp, &rule))
	case http.MethodDelete:
		existed, err := children.DeleteFirewallRule(r.Context(), rp.ResourceName, rp.SubResourceName)
		writeChildDelete(w, existed, err)
	default:
		writeMethodNotAllowed(w)
	}
}

func putFirewallRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, children cachedriver.RedisChildren) {
	var body firewallRuleJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if body.Properties == nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestBody", "properties.startIP and endIP are required")
		return
	}

	rule := cachedriver.FirewallRule{
		Name: rp.SubResourceName, StartIP: body.Properties.StartIP, EndIP: body.Properties.EndIP,
	}

	created, err := children.PutFirewallRule(r.Context(), rp.ResourceName, rule)
	if err != nil {
		writeChildErr(w, rp, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), toFirewallRuleJSON(rp, &rule))
}

func childID(rp *azurearm.ResourcePath, name string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeRedis, rp.ResourceName) +
		"/" + rp.SubResource + "/" + name
}

func toPatchScheduleJSON(
	rp *azurearm.ResourcePath, info *cachedriver.CacheInfo, entries []cachedriver.PatchScheduleEntry,
) patchScheduleJSON {
	out := patchScheduleJSON{
		ID:       childID(rp, patchScheduleName),
		Name:     rp.ResourceName + "/" + patchScheduleName,
		Type:     typePatchSchedules,
		Location: info.Location,
	}

	out.Properties = &struct {
		ScheduleEntries []scheduleEntryJSON `json:"scheduleEntries"`
	}{ScheduleEntries: make([]scheduleEntryJSON, 0, len(entries))}

	for i := range entries {
		hour := entries[i].StartHourUTC
		out.Properties.ScheduleEntries = append(out.Properties.ScheduleEntries, scheduleEntryJSON{
			DayOfWeek: entries[i].DayOfWeek, StartHourUTC: &hour, MaintenanceWindow: entries[i].MaintenanceWindow,
		})
	}

	return out
}

func toFirewallRuleJSON(rp *azurearm.ResourcePath, rule *cachedriver.FirewallRule) firewallRuleJSON {
	out := firewallRuleJSON{
		ID:   childID(rp, rule.Name),
		Name: rp.ResourceName + "/" + rule.Name,
		Type: typeFirewallRules,
	}

	out.Properties = &struct {
		StartIP string `json:"startIP"`
		EndIP   string `json:"endIP"`
	}{StartIP: rule.StartIP, EndIP: rule.EndIP}

	return out
}

func createdStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}

func writeChildNotFound(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
		"The Resource '"+providerName+"/"+typeRedis+"/"+rp.ResourceName+"/"+rp.SubResource+"/"+
			rp.SubResourceName+"' under resource group '"+rp.ResourceGroup+"' was not found.")
}

// writeChildErr maps a child failure: a missing child is ResourceNotFound and
// a validation failure is InvalidRequestBody, as real Azure reports them.
func writeChildErr(w http.ResponseWriter, rp *azurearm.ResourcePath, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeChildNotFound(w, rp)
	case cerrors.IsInvalidArgument(err):
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestBody", cerrors.Message(err))
	default:
		azurearm.WriteCErr(w, err)
	}
}

// writeChildDelete answers a child DELETE: 200 when it existed, 204 when it
// was already absent.
func writeChildDelete(w http.ResponseWriter, existed bool, err error) {
	switch {
	case err != nil:
		azurearm.WriteCErr(w, err)
	case existed:
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
