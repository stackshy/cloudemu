package frontdoor

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// serveOriginGroup routes an originGroups child request. rp.ResourceName is the
// parent profile; rp.SubResourceName is the origin group (empty for a list).
// Origin groups have no PATCH (no tags): armcdn AFDOriginGroups.Update carries only
// properties, so a change is a full PUT.
//
//nolint:dupl // endpoint and origin-group routers are parallel over distinct child types and driver methods.
func (h *Handler) serveOriginGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listOriginGroups(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateOriginGroup(w, r, rp)
	case http.MethodGet:
		h.getOriginGroup(w, r, rp)
	case http.MethodPatch:
		h.updateOriginGroup(w, r, rp)
	case http.MethodDelete:
		h.deleteOriginGroup(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createOrUpdateOriginGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body origGroupJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, created, err := h.fd.CreateOrUpdateOriginGroup(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, buildOriginGroup(&body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toOriginGroupJSON(rp, stored))
}

func (h *Handler) getOriginGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fd.GetOriginGroup(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toOriginGroupJSON(rp, stored))
}

// updateOriginGroup handles PATCH .../originGroups/{og} — AFDOriginGroups.Update.
// Supplied properties keys overlay the stored ones; every other property is left
// untouched.
func (h *Handler) updateOriginGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body origGroupJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fd.GetOriginGroup(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if len(body.Properties) > 0 {
		replaced.Properties = overlayProps(stored.Properties, body.Properties)
	}

	updated, _, err := h.fd.CreateOrUpdateOriginGroup(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toOriginGroupJSON(rp, updated))
}

func (h *Handler) deleteOriginGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.fd.DeleteOriginGroup(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // endpoint and origin-group list are parallel over distinct child types and driver methods.
func (h *Handler) listOriginGroups(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fd.ListOriginGroups(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := origGroupListResult{Value: make([]origGroupJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.SubResourceName = stored[i].Name
		out.Value = append(out.Value, toOriginGroupJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// buildOriginGroup maps the ARM request body to the native store model. There is
// no location and no tags; every property is preserved verbatim except the
// computed stamps (provisioningState, deploymentStatus), which are injected on read.
func buildOriginGroup(body *origGroupJSON) fddriver.AzureFrontDoorOriginGroup {
	return fddriver.AzureFrontDoorOriginGroup{
		Properties: stripKeys(body.Properties, provisioningStateKey, deploymentStatusKey),
	}
}

// toOriginGroupJSON reconstructs the ARM origin-group body from the stored model,
// stamping the id/etag and the computed properties. The origin-group id is the
// parent profile id plus /originGroups/<name>.
func toOriginGroupJSON(rp *azurearm.ResourcePath, g *fddriver.AzureFrontDoorOriginGroup) origGroupJSON {
	profileID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeProfiles, rp.ResourceName)
	id := profileID + "/" + subTypeOrigGroups + "/" + rp.SubResourceName

	return origGroupJSON{
		ID:         id,
		Name:       rp.SubResourceName,
		Type:       origGroupResType,
		Etag:       azurearm.WeakETag(id),
		Properties: assembleOriginGroupProps(g),
	}
}

// assembleOriginGroupProps builds the response properties object: every property
// (loadBalancingSettings, healthProbeSettings, ...) verbatim, then the computed
// provisioningState / deploymentStatus stamps.
func assembleOriginGroupProps(g *fddriver.AzureFrontDoorOriginGroup) map[string]any {
	const injected = 2

	props := make(map[string]any, len(g.Properties)+injected)
	for k, v := range g.Properties {
		props[k] = v
	}

	props[provisioningStateKey] = provisioningStateSucceeded
	props[deploymentStatusKey] = deploymentStatusNotStarted

	return props
}
