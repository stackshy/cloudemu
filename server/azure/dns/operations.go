package dns

import (
	"context"
	"maps"
	"net/http"
	"strings"

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
		SOA:    soaConfigFromProps(body.Properties),
	}

	info, created, err := h.upsertRecord(r, &cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Azure returns 201 Created when the record set is newly created and 200 OK
	// when an existing one is updated (the zone path already distinguishes the
	// two the same way).
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toRecordSetJSON(rp, rp.ResourceName, info))
}

// upsertRecord updates the record if it already exists, otherwise creates it —
// Azure's RecordSets.CreateOrUpdate is upsert semantics. The bool reports
// whether the record set was newly created (true) or updated in place (false),
// so the caller can pick the 201/200 status Azure returns.
func (h *Handler) upsertRecord(r *http.Request, cfg *dnsdriver.RecordConfig) (*dnsdriver.RecordInfo, bool, error) {
	if _, err := h.dns.GetRecord(r.Context(), cfg.ZoneID, cfg.Name, cfg.Type); err == nil {
		info, uerr := h.dns.UpdateRecord(r.Context(), *cfg)
		return info, false, uerr
	}

	info, cerr := h.dns.CreateRecord(r.Context(), *cfg)

	return info, true, cerr
}

// patchZone backs Zones.Update: a PATCH that merges the supplied tags over the
// zone's existing tags (existing tags the caller did not resend are preserved),
// returning 200. Unmodeled zone properties are preserved by the server's overlay
// middleware, which unions a PATCH's fresh unmodeled keys over the stored ones.
func (h *Handler) patchZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body zoneJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	id, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	existing, err := h.dns.GetZone(r.Context(), id)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	merged := maps.Clone(existing.Tags)
	if merged == nil {
		merged = make(map[string]string, len(body.Tags))
	}

	for k, v := range body.Tags {
		merged[k] = v
	}

	info, err := h.dns.UpdateZone(r.Context(), dnsdriver.ZoneConfig{
		Name:    rp.ResourceName,
		Private: existing.Private,
		Tags:    merged,
		Scope:   scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toZoneJSON(rp, info))
}

// patchRecordSet backs RecordSets.Update: a PATCH that merges the supplied
// fields (TTL, the record data for its type, and — for SOA — the editable timing
// fields) over the existing record set, preserving any field the caller omitted
// rather than nil-masking it. Metadata and other unmodeled properties are merged
// by the server's overlay middleware. Returns 200.
func (h *Handler) patchRecordSet(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
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

	existing, err := h.dns.GetRecord(r.Context(), zoneID, name, recordType)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	cfg := dnsdriver.RecordConfig{
		ZoneID: zoneID,
		Name:   name,
		Type:   recordType,
		TTL:    existing.TTL,
		Values: existing.Values,
		SOA:    existing.SOA,
	}

	if body.Properties != nil && body.Properties.TTL != nil {
		cfg.TTL = int(*body.Properties.TTL)
	}

	if recordType == recTypeSOA {
		// SOA host (and email) are system-managed: recordValues yields [host,
		// email], so a timing-only PATCH that omits them would blindly overwrite
		// the stored values with empties and lose the host. Merge instead —
		// preserve the existing host/email unless the PATCH supplies new ones —
		// while the timing fields keep merging via the SOA carrier.
		cfg.Values = mergeSOAValues(existing.Values, body.Properties)
		cfg.SOA = mergeSOAConfig(existing.SOA, soaConfigFromProps(body.Properties))
	} else if supplied := recordValues(recordType, body.Properties); len(supplied) > 0 {
		cfg.Values = supplied
	}

	info, err := h.dns.UpdateRecord(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRecordSetJSON(rp, rp.ResourceName, info))
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
	recordType := recordTypeSegment(rp.SubResource)

	// Azure auto-creates the apex SOA and NS record sets with the zone and
	// forbids deleting them; the zone must own an SOA and its apex NS for its
	// lifetime. Reject the delete rather than orphaning the zone.
	if isApexProtectedRecord(rp.SubResourceName, recordType) {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest",
			"the record set of type "+recordType+" at the zone apex cannot be deleted")
		return
	}

	zoneID, err := h.resolveZoneID(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	derr := h.dns.DeleteRecord(r.Context(), zoneID, rp.SubResourceName, recordType)
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

// listRecordSets backs RecordSets.ListByDnsZone / ListAllByDnsZone: every
// record set in the zone, unfiltered.
func (h *Handler) listRecordSets(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	h.writeRecordSetList(w, r, rp, "")
}

// listRecordSetsByType backs RecordSets.ListByType: the zone's record sets of
// the single type named in the URL (…/dnsZones/{zone}/{type}).
func (h *Handler) listRecordSetsByType(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	h.writeRecordSetList(w, r, rp, recordTypeSegment(rp.SubResource))
}

// writeRecordSetList lists a zone's record sets, optionally filtered to a
// single record type. An empty filterType returns every record set.
func (h *Handler) writeRecordSetList(w http.ResponseWriter, r *http.Request,
	rp *azurearm.ResourcePath, filterType string) {
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
		if filterType != "" && !strings.EqualFold(records[i].Type, filterType) {
			continue
		}

		out = append(out, toRecordSetJSON(rp, rp.ResourceName, &records[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, recordSetListResult{Value: out})
}
