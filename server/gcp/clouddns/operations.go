package clouddns

import (
	"net/http"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func (h *Handler) createZone(w http.ResponseWriter, r *http.Request, rt route) {
	var req managedZoneJSON
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// The dns driver's ZoneInfo models neither dnsName, description, nor
	// creationTime, so they round-trip through reserved tags alongside the user
	// labels. creationTime mirrors Cloud DNS's server-assigned timestamp.
	tags := make(map[string]string, len(req.Labels)+reservedTagCount)
	for k, v := range req.Labels {
		tags[k] = v
	}

	if req.DNSName != "" {
		tags[dnsNameTag] = req.DNSName
	}

	if req.Description != "" {
		tags[descriptionTag] = req.Description
	}

	tags[creationTimeTag] = time.Now().UTC().Format(time.RFC3339)

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

	h.seedApexRecords(r, info)

	gcprest.WriteJSON(w, http.StatusOK, toManagedZoneJSON(info))
}

// seedApexRecords creates the SOA and NS record sets Cloud DNS auto-provisions
// at a new zone's apex, so rrsets.list on a fresh zone returns them (2 records)
// as real Cloud DNS does. A best-effort operation: a record that somehow
// already exists is left as-is rather than failing zone creation.
func (h *Handler) seedApexRecords(r *http.Request, info *dnsdriver.ZoneInfo) {
	dnsName := info.Name
	if v, ok := info.Tags[dnsNameTag]; ok && v != "" {
		dnsName = v
	}

	cfgs := apexRecordConfigs(info.ID, dnsName)
	for i := range cfgs {
		_, _ = h.dns.CreateRecord(r.Context(), cfgs[i])
	}
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

	page, next, ok := paginate(w, r, len(out))
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, managedZonesListResponse{
		Kind:          kindManagedZonesList,
		ManagedZones:  out[page.start:page.end],
		NextPageToken: next,
	})
}

// patchZone serves managedZones.patch/update. Cloud DNS returns an Operation
// (not the zone) from both; the zone's mutable fields — description and labels —
// are applied via the driver, preserving the reserved tags that carry dnsName
// and creationTime.
func (h *Handler) patchZone(w http.ResponseWriter, r *http.Request, rt route) {
	var req managedZoneJSON
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

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

	tags := mergeZoneTags(info.Tags, &req)

	if _, uerr := h.dns.UpdateZone(r.Context(), dnsdriver.ZoneConfig{
		Name:    info.Name,
		Private: info.Private,
		Tags:    tags,
		Scope:   info.Scope,
	}); uerr != nil {
		gcprest.WriteCErr(w, uerr)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{
		Kind:      kindOperation,
		ID:        strconv.FormatUint(h.changeSeq.Add(1), 10),
		StartTime: time.Now().UTC().Format(time.RFC3339),
		Status:    operationStatusDone,
		Type:      "update",
		User:      "cloudemu",
	})
}

// isReservedTag reports whether k is a cloudemu-internal zone tag (dnsName,
// creationTime, description) rather than a user label.
func isReservedTag(k string) bool {
	return k == dnsNameTag || k == creationTimeTag || k == descriptionTag
}

// mergeZoneTags applies a patch/update request's mutable fields onto the zone's
// existing tags: labels replace the user label set when present, description
// updates its reserved tag, and the dnsName/creationTime reserved tags are
// always preserved.
func mergeZoneTags(existing map[string]string, req *managedZoneJSON) map[string]string {
	tags := make(map[string]string, len(existing)+len(req.Labels))

	// Preserve the reserved tags; carry the old user labels forward only when the
	// request omits labels (Cloud DNS replaces the whole label map when set).
	for k, v := range existing {
		if isReservedTag(k) || req.Labels == nil {
			tags[k] = v
		}
	}

	for k, v := range req.Labels {
		tags[k] = v
	}

	if req.Description != "" {
		tags[descriptionTag] = req.Description
	}

	return tags
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

	change := changeJSON{
		Kind:      kindChange,
		ID:        strconv.FormatUint(h.changeSeq.Add(1), 10),
		Additions: req.Additions,
		Deletions: req.Deletions,
		Status:    changeStatusDone,
		StartTime: time.Now().UTC().Format(time.RFC3339),
	}

	h.recordChange(id, &change)

	gcprest.WriteJSON(w, http.StatusOK, change)
}

// recordChange appends an applied change to the zone's change log for later
// retrieval via changes.list/get.
func (h *Handler) recordChange(zoneID string, change *changeJSON) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.changes[zoneID] = append(h.changes[zoneID], *change)
}

// listChanges serves changes.list: the zone's applied change log, oldest first,
// with maxResults/pageToken paging.
func (h *Handler) listChanges(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.project, rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.mu.Lock()
	all := make([]changeJSON, len(h.changes[id]))
	copy(all, h.changes[id])
	h.mu.Unlock()

	page, next, ok := paginate(w, r, len(all))
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, changesListResponse{
		Kind:          kindChangesList,
		Changes:       all[page.start:page.end],
		NextPageToken: next,
	})
}

// getChange serves changes.get: a single applied change by its id.
func (h *Handler) getChange(w http.ResponseWriter, r *http.Request, rt route) {
	id, err := h.resolveZoneID(r.Context(), rt.project, rt.zone)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, c := range h.changes[id] {
		if c.ID == rt.changeID {
			gcprest.WriteJSON(w, http.StatusOK, c)
			return
		}
	}

	gcprest.WriteError(w, http.StatusNotFound, "notFound",
		"change "+rt.changeID+" not found")
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

	// Cloud DNS filters rrsets.list by ?name= (exact) and ?type= before paging.
	nameFilter := r.URL.Query().Get("name")
	typeFilter := r.URL.Query().Get("type")

	out := make([]resourceRecordSetJSON, 0, len(records))

	for i := range records {
		if nameFilter != "" && records[i].Name != nameFilter {
			continue
		}

		if typeFilter != "" && records[i].Type != typeFilter {
			continue
		}

		out = append(out, toRecordSetJSON(&records[i]))
	}

	page, next, ok := paginate(w, r, len(out))
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resourceRecordSetsListResponse{
		Kind:          kindResourceRecordSetsList,
		Rrsets:        out[page.start:page.end],
		NextPageToken: next,
	})
}

// pageRange is the [start,end) slice bounds paginate resolves for a page.
type pageRange struct {
	start int
	end   int
}

// paginate parses Cloud DNS's maxResults/pageToken query params against a result
// of size total and returns the slice bounds for the requested page plus the
// nextPageToken (empty when the page is the last). On a malformed pageToken it
// writes a 400 and returns ok=false; callers must then stop.
func paginate(w http.ResponseWriter, r *http.Request, total int) (pageRange, string, bool) {
	start, err := wire.DecodeOffset(r.URL.Query().Get("pageToken"))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return pageRange{}, "", false
	}

	if start > total {
		start = total
	}

	end := total

	if mr := r.URL.Query().Get("maxResults"); mr != "" {
		n, cerr := strconv.Atoi(mr)
		if cerr == nil && n > 0 && start+n < end {
			end = start + n
		}
	}

	next := ""
	if end < total {
		next = wire.EncodeOffset(end)
	}

	return pageRange{start: start, end: end}, next, true
}
