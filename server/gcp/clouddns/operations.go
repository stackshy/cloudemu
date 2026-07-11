package clouddns

import (
	"net/http"

	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

func (h *Handler) createZone(w http.ResponseWriter, r *http.Request, _ route) {
	var req managedZoneJSON
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.dns.CreateZone(r.Context(), dnsdriver.ZoneConfig{
		Name:    req.Name,
		Private: privateFor(req.Visibility),
		Tags:    req.Labels,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toManagedZoneJSON(info))
}

func (h *Handler) getZone(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	info, err := h.dns.GetZone(r.Context(), id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toManagedZoneJSON(info))
}

func (h *Handler) listZones(w http.ResponseWriter, r *http.Request, _ route) {
	infos, err := h.dns.ListZones(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]managedZoneJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toManagedZoneJSON(&infos[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, managedZonesListResponse{
		Kind:         kindManagedZonesList,
		ManagedZones: out,
	})
}

func (h *Handler) deleteZone(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if derr := h.dns.DeleteZone(r.Context(), id); derr != nil {
		gcprest.WriteCErr(w, derr)
		return
	}

	// Cloud DNS Delete returns an empty 200 body.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

// createChange applies a batch of record additions and deletions atomically,
// mirroring Cloud DNS's Changes.create. Deletions are applied first so a
// replace (delete old + add new) resolves cleanly.
func (h *Handler) createChange(w http.ResponseWriter, r *http.Request, rt route) {
	var req changeJSON
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	id, err := h.resolveZoneID(r.Context(), rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	for i := range req.Deletions {
		d := &req.Deletions[i]
		if derr := h.dns.DeleteRecord(r.Context(), id, d.Name, d.Type); derr != nil {
			gcprest.WriteCErr(w, derr)
			return
		}
	}

	for i := range req.Additions {
		a := &req.Additions[i]

		_, aerr := h.dns.CreateRecord(r.Context(), dnsdriver.RecordConfig{
			ZoneID: id,
			Name:   a.Name,
			Type:   a.Type,
			TTL:    int(a.TTL),
			Values: a.Rrdatas,
		})
		if aerr != nil {
			gcprest.WriteCErr(w, aerr)
			return
		}
	}

	gcprest.WriteJSON(w, http.StatusOK, changeJSON{
		Kind:      kindChange,
		ID:        "1",
		Additions: req.Additions,
		Deletions: req.Deletions,
		Status:    changeStatusDone,
	})
}

func (h *Handler) listRRSets(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	records, err := h.dns.ListRecords(r.Context(), id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]resourceRecordSetJSON, 0, len(records))
	for i := range records {
		out = append(out, toRecordSetJSON(&records[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, resourceRecordSetsListResponse{
		Kind:   kindResourceRecordSetsList,
		Rrsets: out,
	})
}
