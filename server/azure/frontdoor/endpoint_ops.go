package frontdoor

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// serveEndpoint routes an afdEndpoints child request. rp.ResourceName is the
// parent profile; rp.SubResourceName is the endpoint (empty for a list).
//
//nolint:dupl // endpoint and origin-group routers are parallel over distinct child types and driver methods.
func (h *Handler) serveEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listEndpoints(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateEndpoint(w, r, rp)
	case http.MethodGet:
		h.getEndpoint(w, r, rp)
	case http.MethodPatch:
		h.updateEndpoint(w, r, rp)
	case http.MethodDelete:
		h.deleteEndpoint(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createOrUpdateEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body endpointJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, created, err := h.fd.CreateOrUpdateEndpoint(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, buildEndpoint(&body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toEndpointJSON(rp, stored))
}

func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fd.GetEndpoint(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toEndpointJSON(rp, stored))
}

// updateEndpoint handles PATCH .../afdEndpoints/{ep} — AFDEndpoints.Update. Tags
// are REPLACED wholesale when supplied; supplied properties keys (enabledState,
// ...) overlay the stored ones, leaving every other property untouched.
func (h *Handler) updateEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body endpointJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fd.GetEndpoint(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	if len(body.Properties) > 0 {
		replaced.Properties = overlayProps(stored.Properties, body.Properties)
	}

	updated, _, err := h.fd.CreateOrUpdateEndpoint(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toEndpointJSON(rp, updated))
}

func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.fd.DeleteEndpoint(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // endpoint and origin-group list are parallel over distinct child types and driver methods.
func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fd.ListEndpoints(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := endpointListResult{Value: make([]endpointJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.SubResourceName = stored[i].Name
		out.Value = append(out.Value, toEndpointJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// buildEndpoint maps the ARM request body to the native store model. Location and
// tags are top-level; every property is preserved verbatim except the computed
// stamps (hostName, provisioningState, deploymentStatus), which are injected on read.
func buildEndpoint(body *endpointJSON) fddriver.AzureFrontDoorEndpoint {
	return fddriver.AzureFrontDoorEndpoint{
		Location:   orDefaultLocation(body.Location),
		Tags:       body.Tags,
		Properties: stripKeys(body.Properties, hostNameKey, provisioningStateKey, deploymentStatusKey),
	}
}

// toEndpointJSON reconstructs the ARM endpoint body from the stored model,
// stamping the id/etag and the computed properties. The endpoint id is the parent
// profile id plus /afdEndpoints/<name>.
func toEndpointJSON(rp *azurearm.ResourcePath, e *fddriver.AzureFrontDoorEndpoint) endpointJSON {
	profileID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeProfiles, rp.ResourceName)
	id := profileID + "/" + subTypeEndpoints + "/" + rp.SubResourceName

	return endpointJSON{
		ID:         id,
		Name:       rp.SubResourceName,
		Type:       endpointResourceType,
		Location:   orDefaultLocation(e.Location),
		Etag:       azurearm.WeakETag(id),
		Tags:       e.Tags,
		Properties: assembleEndpointProps(profileID, rp.SubResourceName, e),
	}
}

// assembleEndpointProps builds the response properties object: every deferred
// property verbatim, the enabledState / autoGeneratedDomainNameLabelScope defaults,
// and the computed hostName / provisioningState / deploymentStatus stamps.
func assembleEndpointProps(profileID, name string, e *fddriver.AzureFrontDoorEndpoint) map[string]any {
	const injected = 5

	props := make(map[string]any, len(e.Properties)+injected)
	for k, v := range e.Properties {
		props[k] = v
	}

	if _, ok := props[enabledStateKey]; !ok {
		props[enabledStateKey] = enabledStateDefault
	}

	if _, ok := props[autoGenDomainScopeKey]; !ok {
		props[autoGenDomainScopeKey] = autoGenDomainScopeDefault
	}

	props[hostNameKey] = endpointHostName(profileID, name)
	props[provisioningStateKey] = provisioningStateSucceeded
	props[deploymentStatusKey] = deploymentStatusNotStarted

	return props
}
