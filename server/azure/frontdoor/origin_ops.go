package frontdoor

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// serveOrigin routes .../originGroups/{og}/origins[/{o}] (AFDOrigins.*). The
// handlers only decode the body and map errors; validation, defaults and the
// last-enabled-origin rule live in the provider.
func (h *Handler) serveOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	dispatchNested(w, r, np, &nestedOps{
		put: h.createOrUpdateOrigin, get: h.getOrigin, patch: h.updateOrigin,
		del: h.deleteOrigin, list: h.listOrigins,
	})
}

// createOrUpdateOrigin handles PUT (AFDOrigins.BeginCreate): a full replace.
func (h *Handler) createOrUpdateOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	var body nestedJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, created, err := h.fd.CreateOrUpdateOrigin(r.Context(), np.rg, np.profile, np.parent, np.name,
		fddriver.AzureFrontDoorOrigin{Properties: stripKeys(body.Properties, originComputedKeys()...)})
	if err != nil {
		writePutErr(w, err)
		return
	}

	writeCreated(w, created, toOriginJSON(np.sub, stored))
}

// updateOrigin handles PATCH (AFDOrigins.BeginUpdate): the provider overlays the
// supplied property keys on the stored ones and re-validates the merge.
func (h *Handler) updateOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	var body nestedJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fd.UpdateOrigin(r.Context(), np.rg, np.profile, np.parent, np.name,
		stripKeys(body.Properties, originComputedKeys()...))
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toOriginJSON(np.sub, stored))
}

func (h *Handler) getOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.GetOrigin(r.Context(), np.rg, np.profile, np.parent, np.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toOriginJSON(np.sub, stored))
}

func (h *Handler) deleteOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	writeDeleteResult(w, h.fd.DeleteOrigin(r.Context(), np.rg, np.profile, np.parent, np.name))
}

//nolint:dupl // parallel to listRoutes over distinct grandchild types and driver methods.
func (h *Handler) listOrigins(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.ListOrigins(r.Context(), np.rg, np.profile, np.parent)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := nestedListResult{Value: make([]nestedJSON, 0, len(stored))}
	for i := range stored {
		out.Value = append(out.Value, toOriginJSON(np.sub, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// originComputedKeys are the read-only origin properties Azure computes; a
// client echo of them is dropped so the stored copy never goes stale.
func originComputedKeys() []string {
	return []string{provisioningStateKey, deploymentStatusKey, originGroupNameKey}
}

// toOriginJSON reconstructs the ARM origin body from the stored origin (so the
// id, name and originGroupName carry the stored casing), stamping the computed
// read-only properties.
func toOriginJSON(sub string, o *fddriver.AzureFrontDoorOrigin) nestedJSON {
	id := grandchildID(sub, o.ResourceGroup, o.Profile, subTypeOrigGroups, o.OriginGroup, subTypeOrigins, o.Name)

	return nestedJSON{
		ID:   id,
		Name: o.Name,
		Type: originResourceType,
		Etag: storedETag(o.ETag, id),
		Properties: withComputed(o.Properties, map[string]any{
			originGroupNameKey:   o.OriginGroup,
			provisioningStateKey: provisioningStateSucceeded,
			deploymentStatusKey:  deploymentStatusNotStarted,
		}),
	}
}
