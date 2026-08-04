package clouddns

import (
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func (h *Handler) createZone(w http.ResponseWriter, r *http.Request, rt route) {
	var req managedZoneJSON
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	tags := req.Labels
	if req.DNSName != "" {
		tags = make(map[string]string, len(req.Labels)+1)
		for k, v := range req.Labels {
			tags[k] = v
		}

		tags[dnsNameTag] = req.DNSName
	}

	info, err := h.dns.CreateZone(r.Context(), dnsdriver.ZoneConfig{
		Name:    req.Name,
		Private: privateFor(req.Visibility),
		Tags:    tags,
		Scope:   scope.Scope{Project: rt.project},
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toManagedZoneJSON(info))
}

func (h *Handler) getZone(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.project, rt.zone)
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

func (h *Handler) listZones(w http.ResponseWriter, r *http.Request, rt route) {
	infos, err := h.dns.ListZones(r.Context(), scope.Scope{Project: rt.project})
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
	id, err := h.resolveZoneID(r.Context(), rt.project, rt.zone)
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

	id, err := h.resolveZoneID(r.Context(), rt.project, rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Cloud DNS applies a change atomically. The dns driver has no batch/
	// transaction primitive, so validate the whole batch up front — every
	// deletion must resolve and no addition may already exist — before any
	// mutation. This makes a bad batch fail cleanly without half-applying it.
	// (A concurrent writer between validation and apply could still race; a
	// true fix needs a driver-level transaction — tracked as a follow-up.)
	for i := range req.Deletions {
		d := &req.Deletions[i]
		if _, gerr := h.dns.GetRecord(r.Context(), id, d.Name, d.Type); gerr != nil {
			gcprest.WriteCErr(w, gerr)
			return
		}
	}

	// The canonical "update a record set" change deletes the old rrset and adds
	// a new one with the SAME name+type in one batch. Such an addition is not a
	// real conflict — it replaces a record this same change removes — so exempt
	// additions whose (name,type) also appears in the deletions.
	deleting := make(map[string]bool, len(req.Deletions))
	for i := range req.Deletions {
		deleting[rrsetKey(req.Deletions[i].Name, req.Deletions[i].Type)] = true
	}

	for i := range req.Additions {
		a := &req.Additions[i]
		if deleting[rrsetKey(a.Name, a.Type)] {
			continue
		}

		if _, gerr := h.dns.GetRecord(r.Context(), id, a.Name, a.Type); gerr == nil {
			gcprest.WriteCErr(w, cerrors.Newf(cerrors.AlreadyExists,
				"record set %q %s already exists", a.Name, a.Type))
			return
		}
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
		ID:        strconv.FormatUint(h.changeSeq.Add(1), 10),
		Additions: req.Additions,
		Deletions: req.Deletions,
		Status:    changeStatusDone,
	})
}

// rrsetKey identifies a record set by name+type within a zone.
func rrsetKey(name, rtype string) string {
	return name + "|" + rtype
}

func (h *Handler) listRRSets(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.project, rt.zone)
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
