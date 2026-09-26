package frontdoor

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// serveOrigin routes .../originGroups/{og}/origins[/{o}] (AFDOrigins.*).
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

	h.storeOrigin(w, r, np, stripKeys(body.Properties, originComputedKeys()...))
}

// updateOrigin handles PATCH (AFDOrigins.BeginUpdate): supplied property keys
// overlay the stored ones and the merged origin is re-validated.
//
//nolint:dupl // parallel to updateRoute over distinct grandchild types and driver methods.
func (h *Handler) updateOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	var body nestedJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fd.GetOrigin(r.Context(), np.rg, np.profile, np.parent, np.name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	patch := stripKeys(body.Properties, originComputedKeys()...)
	h.storeOrigin(w, r, np, overlayProps(stored.Properties, patch))
}

// storeOrigin validates props and writes the origin, answering 201 on create and
// 200 on replace.
func (h *Handler) storeOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath, props map[string]any) {
	if err := validateOrigin(props); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	stored, created, err := h.fd.CreateOrUpdateOrigin(
		r.Context(), np.rg, np.profile, np.parent, np.name, fddriver.AzureFrontDoorOrigin{Properties: props})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeCreated(w, created, toOriginJSON(np, stored))
}

func (h *Handler) getOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.GetOrigin(r.Context(), np.rg, np.profile, np.parent, np.name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toOriginJSON(np, stored))
}

func (h *Handler) deleteOrigin(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	if err := h.fd.DeleteOrigin(r.Context(), np.rg, np.profile, np.parent, np.name); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // parallel to listRoutes over distinct grandchild types and driver methods.
func (h *Handler) listOrigins(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.ListOrigins(r.Context(), np.rg, np.profile, np.parent)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := nestedListResult{Value: make([]nestedJSON, 0, len(stored))}
	for i := range stored {
		out.Value = append(out.Value, toOriginJSON(np.withName(stored[i].Name), &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// originComputedKeys are the read-only origin properties Azure computes; a
// client echo of them is dropped so the stored copy never goes stale.
func originComputedKeys() []string {
	return []string{provisioningStateKey, deploymentStatusKey, originGroupNameKey}
}

// validateOrigin applies the checks Azure makes on an origin: hostName is
// required, and the ports, priority and weight must sit in their documented
// ranges when supplied.
func validateOrigin(props map[string]any) error {
	if s, _ := props[hostNameKey].(string); s == "" {
		return cerrors.New(cerrors.InvalidArgument, "front door origin properties.hostName is required")
	}

	ranges := []struct {
		key      string
		min, max float64
	}{
		{httpPortKey, minPort, maxPort},
		{httpsPortKey, minPort, maxPort},
		{priorityKey, minPriority, maxPriority},
		{weightKey, minWeight, maxWeight},
	}

	for _, rg := range ranges {
		if err := checkRange(props, rg.key, rg.min, rg.max); err != nil {
			return err
		}
	}

	return nil
}

// checkRange rejects a numeric property outside [lo, hi] or of the wrong type.
// An absent key passes.
func checkRange(props map[string]any, key string, lo, hi float64) error {
	v, ok := props[key]
	if !ok || v == nil {
		return nil
	}

	n, isNum := v.(float64)
	if !isNum || n < lo || n > hi || n != float64(int64(n)) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"front door origin properties.%s must be an integer between %d and %d", key, int64(lo), int64(hi))
	}

	return nil
}

// toOriginJSON reconstructs the ARM origin body, stamping the id/etag, the
// defaults Azure reports for omitted fields, and the computed stamps.
func toOriginJSON(np *nestedPath, o *fddriver.AzureFrontDoorOrigin) nestedJSON {
	id := np.id(subTypeOrigGroups, subTypeOrigins, np.name)

	const injected = 7

	props := copyProps(o.Properties, injected)
	setDefault(props, enabledStateKey, enabledStateDefault)
	setDefault(props, httpPortKey, defaultHTTPPort)
	setDefault(props, httpsPortKey, defaultHTTPSPort)
	setDefault(props, enforceCertNameCheckKey, true)

	props[originGroupNameKey] = np.parent
	props[provisioningStateKey] = provisioningStateSucceeded
	props[deploymentStatusKey] = deploymentStatusNotStarted

	return nestedJSON{
		ID:         id,
		Name:       np.name,
		Type:       originResourceType,
		Etag:       azurearm.WeakETag(id),
		Properties: props,
	}
}
