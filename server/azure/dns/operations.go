package dns

import (
	"net/http"

	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
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

	// CreateOrUpdate is idempotent: if the zone already exists, echo it back.
	if id, err := h.resolveZoneID(r.Context(), rp.ResourceName); err == nil {
		info, gerr := h.dns.GetZone(r.Context(), id)
		if gerr != nil {
			azurearm.WriteCErr(w, gerr)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toZoneJSON(rp, info))

		return
	}

	info, err := h.dns.CreateZone(r.Context(), dnsdriver.ZoneConfig{
		Name:    rp.ResourceName,
		Private: private,
		Tags:    body.Tags,
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toZoneJSON(rp, info))
}

func (h *Handler) getZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	id, err := h.resolveZoneID(r.Context(), rp.ResourceName)
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
// 200 with an empty body completes the poller on the first response.
func (h *Handler) deleteZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	id, err := h.resolveZoneID(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if derr := h.dns.DeleteZone(r.Context(), id); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listZones(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	infos, err := h.dns.ListZones(r.Context())
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

	zoneID, err := h.resolveZoneID(r.Context(), rp.ResourceName)
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
	zoneID, err := h.resolveZoneID(r.Context(), rp.ResourceName)
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
	zoneID, err := h.resolveZoneID(r.Context(), rp.ResourceName)
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
	zoneID, err := h.resolveZoneID(r.Context(), rp.ResourceName)
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
