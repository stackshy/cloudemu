package dns

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// --- zones ---

func (h *Handler) createOrUpdateZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body zoneJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	private := false
	if body.Properties != nil {
		private = privateFromZoneType(body.Properties.ZoneType)
	}

	cfg := dnsdriver.ZoneConfig{
		Name:    rp.ResourceName,
		Private: private,
		Tags:    body.Tags,
		Scope:   scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}

	// CreateOrUpdate is upsert. Try to update a zone that already exists in
	// THIS scope; only create when none does. Matching by scope (not just
	// name) means a same-named zone in another resource group is never
	// hijacked — it stays a distinct zone.
	if info, uerr := h.dns.UpdateZone(r.Context(), cfg); uerr == nil {
		azurearm.WriteJSON(w, http.StatusOK, toZoneJSON(rp, info))
		return
	} else if !cerrors.IsNotFound(uerr) {
		azurearm.WriteCErr(w, uerr)
		return
	}

	info, err := h.dns.CreateZone(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Azure auto-provisions the apex SOA and NS record sets when a zone is
	// created, so a fresh public zone already reports numberOfRecordSets=2 and
	// its ListByDnsZone is non-empty. Refresh info so the response carries the
	// updated record count.
	if refreshed := h.provisionApexRecords(r.Context(), info); refreshed != nil {
		info = refreshed
	}

	azurearm.WriteJSON(w, http.StatusCreated, toZoneJSON(rp, info))
}

// provisionApexRecords creates the apex SOA and NS record sets Azure DNS
// auto-generates with every new zone. It returns the zone re-read with the
// bumped record count, or nil if the zone could not be re-read (leaving the
// caller's copy in place). Private zones get no name-server-backed records.
func (h *Handler) provisionApexRecords(ctx context.Context, zone *dnsdriver.ZoneInfo) *dnsdriver.ZoneInfo {
	nameServers := zoneNameServers(zone.Name, zone.Private)
	if len(nameServers) == 0 {
		return nil
	}

	_, _ = h.dns.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID, Name: apexRecordName, Type: recTypeNS, TTL: nsRecordTTL, Values: nameServers,
	})
	_, _ = h.dns.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID, Name: apexRecordName, Type: recTypeSOA, TTL: soaRecordTTL,
		Values: []string{nameServers[0], soaEmail},
	})

	refreshed, err := h.dns.GetZone(ctx, zone.ID)
	if err != nil {
		return nil
	}

	return refreshed
}

func (h *Handler) getZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	id, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	info, err := h.dns.GetZone(r.Context(), id)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toZoneJSON(rp, info))
}

// deleteZone removes the zone. Zones.Delete is an LRO in the SDK; returning
// 200 with an empty body completes the poller on the first response. A missing
// zone makes the ARM DELETE idempotent: 204 No Content ("The DNS zone was not
// found"), not a 404 error body.
func (h *Handler) deleteZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	id, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		if cerrors.IsNotFound(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	if derr := h.dns.DeleteZone(r.Context(), id); derr != nil {
		if cerrors.IsNotFound(derr) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		azurearm.WriteCErr(w, derr)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listZones(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	infos, err := h.dns.ListZones(r.Context(),
		scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]zoneJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toZoneJSON(rp, &infos[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, zoneListResult{Value: out})
}

// --- record sets ---

func (h *Handler) createOrUpdateRecordSet(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body recordSetJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	zoneID, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	recordType := recordTypeSegment(rp.SubResource)
	name := rp.SubResourceName

	cfg := dnsdriver.RecordConfig{
		ZoneID: zoneID,
		Name:   name,
		Type:   recordType,
		TTL:    ttlOrDefault(body.Properties),
		Values: recordValues(recordType, body.Properties),
	}

	info, err := h.upsertRecord(r, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toRecordSetJSON(rp, rp.ResourceName, info))
}

// upsertRecord updates the record if it already exists, otherwise creates it —
// Azure's RecordSets.CreateOrUpdate is upsert semantics.
func (h *Handler) upsertRecord(r *http.Request, cfg dnsdriver.RecordConfig) (*dnsdriver.RecordInfo, error) {
	if _, err := h.dns.GetRecord(r.Context(), cfg.ZoneID, cfg.Name, cfg.Type); err == nil {
		return h.dns.UpdateRecord(r.Context(), cfg)
	}

	return h.dns.CreateRecord(r.Context(), cfg)
}

func (h *Handler) getRecordSet(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	zoneID, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	info, err := h.dns.GetRecord(r.Context(), zoneID, rp.SubResourceName, recordTypeSegment(rp.SubResource))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRecordSetJSON(rp, rp.ResourceName, info))
}

func (h *Handler) deleteRecordSet(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	zoneID, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	derr := h.dns.DeleteRecord(r.Context(), zoneID, rp.SubResourceName, recordTypeSegment(rp.SubResource))
	if derr != nil && !cerrors.IsNotFound(derr) {
		azurearm.WriteCErr(w, derr)
		return
	}

	// Azure returns 200 for a delete and 204 when the record set was already
	// absent; either terminates the SDK call cleanly.
	if cerrors.IsNotFound(derr) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listRecordSets(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	zoneID, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	records, err := h.dns.ListRecords(r.Context(), zoneID)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]recordSetJSON, 0, len(records))
	for i := range records {
		out = append(out, toRecordSetJSON(rp, rp.ResourceName, &records[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, recordSetListResult{Value: out})
}
