package privatedns

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	pddriver "github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

// createOrUpdateZone handles PUT .../privateDnsZones/{zone}. The whole zone
// arrives in one body and fully REPLACES the stored state (ARM CreateOrUpdate).
// PrivateZones.CreateOrUpdate is an LRO; returning the fully-provisioned body
// completes the poller on the first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdateZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body zoneJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	zone := pddriver.PrivateZone{Location: defaultLocation, Tags: body.Tags}

	stored, created, err := h.pdns.CreateOrUpdatePrivateZone(r.Context(), rp.ResourceGroup, rp.ResourceName, zone)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toZoneJSON(rp, stored))
}

func (h *Handler) getZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.GetPrivateZone(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toZoneJSON(rp, stored))
}

// updateZoneTags handles PATCH .../privateDnsZones/{zone} — PrivateZones.Update.
// Real armprivatedns UpdateTags REPLACES the tag collection wholesale; every
// other property is left untouched. A request with tags omitted (nil map) is a
// no-op.
func (h *Handler) updateZoneTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body zoneJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.pdns.GetPrivateZone(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := pddriver.PrivateZone{Location: stored.Location, Tags: stored.Tags}
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	updated, _, err := h.pdns.CreateOrUpdatePrivateZone(r.Context(), rp.ResourceGroup, rp.ResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toZoneJSON(rp, updated))
}

// deleteZone removes the zone (cascading to its links and records).
// PrivateZones.Delete is an LRO; a 200 with empty body completes the poller.
func (h *Handler) deleteZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.pdns.DeletePrivateZone(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listZones(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.ListPrivateZones(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := zoneListResult{Value: make([]zoneJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toZoneJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// toZoneJSON reconstructs the ARM zone body from the stored model, stamping the
// id/etag, the fixed quota constants, the computed counts and the terminal
// provisioningState. Location is always reported as "global".
func toZoneJSON(rp *azurearm.ResourcePath, zone *pddriver.PrivateZone) zoneJSON {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePrivateZones, rp.ResourceName)

	return zoneJSON{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     zoneResourceType,
		Location: defaultLocation,
		Etag:     azurearm.ETag(id),
		Tags:     zone.Tags,
		Properties: &zonePropsJSON{
			MaxNumberOfRecordSets:                          maxNumberOfRecordSets,
			NumberOfRecordSets:                             zone.NumberOfRecordSets,
			MaxNumberOfVirtualNetworkLinks:                 maxNumberOfVirtualNetworkLinks,
			NumberOfVirtualNetworkLinks:                    zone.NumberOfVirtualNetworkLinks,
			MaxNumberOfVirtualNetworkLinksWithRegistration: maxNumberOfVirtualNetworkLinksWithRegistration,
			NumberOfVirtualNetworkLinksWithRegistration:    zone.NumberOfVirtualNetworkLinksWithRegistration,
			ProvisioningState:                              provisioningStateSucceeded,
		},
	}
}
