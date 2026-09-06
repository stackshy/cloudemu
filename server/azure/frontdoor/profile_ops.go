package frontdoor

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// createOrUpdateProfile handles PUT .../profiles/{name}. The whole profile arrives
// in one body and fully REPLACES the stored state (ARM CreateOrUpdate). Profiles
// is an LRO; returning the fully-provisioned body completes the poller on the
// first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdateProfile(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body profileJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, created, err := h.fd.CreateOrUpdateProfile(r.Context(), rp.ResourceGroup, rp.ResourceName, buildProfile(&body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toProfileJSON(rp, stored))
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fd.GetProfile(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toProfileJSON(rp, stored))
}

// updateProfileTags handles PATCH .../profiles/{name} — Profiles.Update. Real
// armcdn Update REPLACES the tag collection wholesale (an omitted existing key is
// dropped); every other property is left untouched. A request with tags entirely
// omitted (nil map) is a no-op.
func (h *Handler) updateProfileTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body profileJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fd.GetProfile(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	updated, _, err := h.fd.CreateOrUpdateProfile(r.Context(), rp.ResourceGroup, rp.ResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toProfileJSON(rp, updated))
}

// deleteProfile removes the profile (cascading its children). Profiles.Delete is
// an LRO; a 200 with empty body completes the poller.
func (h *Handler) deleteProfile(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.fd.DeleteProfile(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fd.ListProfiles(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := profileListResult{Value: make([]profileJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toProfileJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// buildProfile maps the ARM request body to the native store model. sku, location
// and identity are top-level; every property key is preserved verbatim in
// OtherProps except the computed stamps, which are always injected on read.
func buildProfile(body *profileJSON) fddriver.AzureFrontDoorProfile {
	p := fddriver.AzureFrontDoorProfile{
		Location:   orDefaultLocation(body.Location),
		Identity:   body.Identity,
		Tags:       body.Tags,
		OtherProps: stripKeys(body.Properties, provisioningStateKey, resourceStateKey, frontDoorIDKey),
	}

	if body.SKU != nil {
		p.SKUName = body.SKU.Name
	}

	return p
}

// toProfileJSON reconstructs the ARM profile body from the stored model, stamping
// the id/etag, the top-level sku/kind and the computed properties. rp is
// authoritative for the id (taken from the URL).
func toProfileJSON(rp *azurearm.ResourcePath, p *fddriver.AzureFrontDoorProfile) profileJSON {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeProfiles, rp.ResourceName)

	out := profileJSON{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       profileResourceType,
		Location:   orDefaultLocation(p.Location),
		Kind:       kindFrontDoor,
		Identity:   p.Identity,
		Etag:       azurearm.WeakETag(id),
		Tags:       p.Tags,
		Properties: assembleProfileProps(id, p),
	}

	if p.SKUName != "" {
		out.SKU = &skuJSON{Name: p.SKUName}
	}

	return out
}

// assembleProfileProps builds the response properties object: every deferred
// property verbatim, then the modeled originResponseTimeoutSeconds default and the
// computed frontDoorId / provisioningState / resourceState stamps.
func assembleProfileProps(id string, p *fddriver.AzureFrontDoorProfile) map[string]any {
	const injected = 4

	props := make(map[string]any, len(p.OtherProps)+injected)
	for k, v := range p.OtherProps {
		props[k] = v
	}

	if _, ok := props[originResponseTimeoutKey]; !ok {
		props[originResponseTimeoutKey] = originResponseTimeoutDefault
	}

	props[frontDoorIDKey] = idgen.SyntheticGUID(id)
	props[provisioningStateKey] = provisioningStateSucceeded
	props[resourceStateKey] = resourceStateActive

	return props
}
