package containerinstances

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateGroup handles PUT — ContainerGroups.BeginCreateOrUpdate. The LRO
// completes inline: returning 201 with the resource body terminates the SDK's
// poller on the first response.
func (h *Handler) createOrUpdateGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body containerGroupJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	group, err := h.aci.CreateContainerGroup(r.Context(), toConfig(rp, &body))
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toGroupJSON(rp, group))
}

// getGroup handles GET on a single resource — ContainerGroups.Get.
func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	group, err := h.aci.GetContainerGroup(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toGroupJSON(rp, group))
}

// deleteGroup handles DELETE — ContainerGroups.BeginDelete. Returning 200 with
// the deleted resource body completes the SDK's poller on the first response.
func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	group, err := h.aci.GetContainerGroup(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	if derr := h.aci.DeleteContainerGroup(r.Context(), rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toGroupJSON(rp, group))
}

// listGroups handles GET on the collection — ContainerGroups.ListByResourceGroup
// / List. The filter carries the path's subscription and, for RG-level lists,
// its resource group.
func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	groups, err := h.aci.ListContainerGroups(r.Context(),
		scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup})
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]containerGroupJSON, 0, len(groups))
	for i := range groups {
		out = append(out, toGroupJSON(rp, &groups[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, containerGroupListResult{Value: out})
}

// containerLogs handles GET .../containers/{c}/logs — Containers.ListLogs. The
// container name is the sub-resource name; an optional tail query caps the
// returned lines.
func (h *Handler) containerLogs(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)

		return
	}

	content, err := h.aci.ContainerLogs(r.Context(), rp.ResourceName, rp.SubResourceName, tailParam(r))
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, logsJSON{Content: content})
}

// tailParam reads the optional ?tail=N query. A missing or invalid value yields
// 0, meaning "all available logs".
func tailParam(r *http.Request) int {
	raw := r.URL.Query().Get("tail")
	if raw == "" {
		return 0
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}

	return n
}
