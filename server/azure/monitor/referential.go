package monitor

import (
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// validateActionGroupRefs rejects a metricAlert create/update whose
// properties.actions[].actionGroupId does not resolve to a stored actionGroups
// resource, mirroring real Azure Monitor's rejection of a metric alert rule
// linked to an action group that doesn't exist (the same reference-validation
// convention already applied to gcplb/azurelb create/update: 400
// InvalidArgument, not a silent no-op deferred to breach time). ARM resource
// ids are case-insensitive, so this resolves each id by scanning the stored
// action groups and comparing canonical ids with strings.EqualFold — the same
// pattern actionGroupInUse uses below — rather than an exact-case store.get(),
// which would reject a reference that differs only in casing even though
// RegisterActionGroup/fireActionGroups (providers/azure/monitor/actiongroups.go)
// already resolve such a reference case-insensitively at breach time.
func (h *Handler) validateActionGroupRefs(ids []string) error {
	for _, id := range ids {
		agRP, ok := azurearm.ParsePath(id)
		if !ok || canonicalType(agRP.ResourceType) != typeActionGroup || agRP.ResourceName == "" {
			return cerrors.Newf(cerrors.InvalidArgument, "actionGroupId %q is not a valid action group resource id", id)
		}

		if !h.actionGroupExists(id) {
			return cerrors.Newf(cerrors.InvalidArgument, "action group %q not found", id)
		}
	}

	return nil
}

// actionGroupExists reports whether agID resolves to a stored actionGroups
// resource, comparing canonical ARM ids case-insensitively.
func (h *Handler) actionGroupExists(agID string) bool {
	for key := range h.store.allOfKind(typeActionGroup) {
		candidate := azurearm.BuildResourceID(key.subscription, key.resourceGroup, providerName, typeActionGroup, key.name)
		if strings.EqualFold(candidate, agID) {
			return true
		}
	}

	return false
}

// activityLogActionGroupIDs extracts properties.actions.actionGroups[].
// actionGroupId from an activityLogAlert definition — the nested shape real
// Microsoft.Insights/activityLogAlerts use to link action groups, distinct
// from a metricAlert's flat properties.actions[].actionGroupId.
func activityLogActionGroupIDs(props map[string]any) []string {
	actions, ok := props["actions"].(map[string]any)
	if !ok {
		return nil
	}

	groups, ok := actions["actionGroups"].([]any)
	if !ok {
		return nil
	}

	ids := make([]string, 0, len(groups))

	for _, g := range groups {
		item, ok := g.(map[string]any)
		if !ok {
			continue
		}

		if id := stringField(item, "actionGroupId"); id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}

// actionGroupInUse reports what still references the action group with ARM
// resource id agID — a metric alert or an activity-log alert — across the
// whole store (not scoped to the action group's own resource group, since a
// referencing alert can live in a different one). Returns "" when nothing
// references it, matching the azurelb poolReferencedBy convention: an empty
// string means "safe to delete".
func (h *Handler) actionGroupInUse(agID string) string {
	for key, res := range h.store.allOfKind(typeAlerts) {
		if idsContain(actionGroupIDs(res.Properties), agID) {
			return fmt.Sprintf("metric alert %q", key.name)
		}
	}

	for key, res := range h.store.allOfKind(typeActivityLog) {
		if idsContain(activityLogActionGroupIDs(res.Properties), agID) {
			return fmt.Sprintf("activity log alert %q", key.name)
		}
	}

	return ""
}

// idsContain reports whether target appears in ids, comparing ARM resource ids
// case-insensitively (real ARM ids are case-insensitive).
func idsContain(ids []string, target string) bool {
	for _, id := range ids {
		if strings.EqualFold(id, target) {
			return true
		}
	}

	return false
}
