package clouddns

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
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

	if !validateZoneCreate(w, &req) {
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
		Name:               req.Name,
		Private:            privateFor(req.Visibility),
		Tags:               tags,
		Scope:              scope.Scope{Project: rt.project},
		DNSSECConfig:       dnssecFromJSON(req.DnssecConfig),
		VisibilityNetworks: visibilityFromJSON(req.PrivateVisibilityConfig),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.seedApexRecords(r, info)

	gcprest.WriteJSON(w, http.StatusOK, toManagedZoneJSON(info))
}

// validateZoneCreate enforces Cloud DNS's managed-zone create constraints: the
// zone name must match [a-z]([-a-z0-9]*[a-z0-9])?, and dnsName must be present
// and a fully qualified name ending in a trailing dot. On a violation it writes
// a 400 and returns false so the caller stops.
func validateZoneCreate(w http.ResponseWriter, req *managedZoneJSON) bool {
	if !isValidZoneName(req.Name) {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"the managed zone name must match [a-z]([-a-z0-9]*[a-z0-9])?")
		return false
	}

	if req.DNSName == "" || !strings.HasSuffix(req.DNSName, ".") {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"dnsName is required and must be a fully qualified name ending with a dot")
		return false
	}

	return true
}

// isValidZoneName reports whether name matches Cloud DNS's managed-zone name
// grammar [a-z]([-a-z0-9]*[a-z0-9])?: a lowercase-letter start, lowercase
// alphanumeric or hyphen body, and no trailing hyphen.
func isValidZoneName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}

	for i := 1; i < len(name); i++ {
		c := name[i]

		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'

		if !isLower && !isDigit && c != '-' {
			return false
		}
	}

	last := name[len(name)-1]

	return last != '-'
}

// seedApexRecords creates the SOA and NS record sets Cloud DNS auto-provisions
// at a new zone's apex, so rrsets.list on a fresh zone returns them (2 records)
// as real Cloud DNS does, and logs the initial change (id "0") that Cloud DNS
// records for that provisioning so changes.list on a fresh zone returns it. A
// best-effort create: a record that somehow already exists is left as-is rather
// than failing zone creation.
func (h *Handler) seedApexRecords(r *http.Request, info *dnsdriver.ZoneInfo) {
	cfgs := apexRecordConfigs(info.ID, apexDNSName(info))
	additions := make([]resourceRecordSetJSON, 0, len(cfgs))

	for i := range cfgs {
		_, _ = h.dns.CreateRecord(r.Context(), cfgs[i])
		additions = append(additions, recordConfigToJSON(&cfgs[i]))
	}

	change := changeJSON{
		Kind:      kindChange,
		Additions: additions,
		Status:    changeStatusDone,
		StartTime: time.Now().UTC().Format(time.RFC3339),
	}
	h.recordChange(info.ID, &change)
}

// apexDNSName returns the zone's DNS suffix (the reserved dnsName tag), falling
// back to the zone name when the tag is absent.
func apexDNSName(info *dnsdriver.ZoneInfo) string {
	if v, ok := info.Tags[dnsNameTag]; ok && v != "" {
		return v
	}

	return info.Name
}

// recordConfigToJSON renders a driver RecordConfig as the wire rrset shape, used
// to describe the apex records in the seeded initial change.
func recordConfigToJSON(c *dnsdriver.RecordConfig) resourceRecordSetJSON {
	return resourceRecordSetJSON{
		Kind:    kindResourceRecordSet,
		Name:    c.Name,
		Type:    c.Type,
		TTL:     int64(c.TTL),
		Rrdatas: c.Values,
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
		Name:               info.Name,
		Private:            info.Private,
		Tags:               tags,
		Scope:              info.Scope,
		DNSSECConfig:       dnssecFromJSON(req.DnssecConfig),
		VisibilityNetworks: visibilityFromJSON(req.PrivateVisibilityConfig),
	}); uerr != nil {
		gcprest.WriteCErr(w, uerr)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{
		Kind:      kindOperation,
		ID:        strconv.FormatUint(h.opSeq.Add(1), 10),
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

	// Hold applyMu across the empty-check and the delete so a concurrent
	// changes.create cannot slip a record into the zone between the two.
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	// Cloud DNS refuses to delete a zone that still holds user record sets; only
	// the auto-created apex SOA/NS may remain. Anything else → 400 containerNotEmpty.
	if ok := h.rejectIfNotEmpty(w, r, id); !ok {
		return
	}

	if derr := h.dns.DeleteZone(r.Context(), id); derr != nil {
		gcprest.WriteCErr(w, derr)
		return
	}

	// Cloud DNS Delete returns an empty 200 body.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

// rejectIfNotEmpty writes a 400 containerNotEmpty and returns false when the
// zone holds any record set beyond the apex SOA/NS Cloud DNS auto-creates.
func (h *Handler) rejectIfNotEmpty(w http.ResponseWriter, r *http.Request, id string) bool {
	info, err := h.dns.GetZone(r.Context(), id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return false
	}

	records, err := h.dns.ListRecords(r.Context(), id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return false
	}

	dnsName := apexDNSName(info)
	for i := range records {
		if !isApexRRSet(records[i].Name, records[i].Type, dnsName) {
			gcprest.WriteError(w, http.StatusBadRequest, "containerNotEmpty",
				"the managed zone still has user-created record sets and cannot be deleted")
			return false
		}
	}

	return true
}

// isApexRRSet reports whether name/rtype is the zone's apex NS or SOA record set,
// which Cloud DNS auto-creates and protects from direct deletion.
func isApexRRSet(name, rtype, dnsName string) bool {
	return name == dnsName && (rtype == "NS" || rtype == "SOA")
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

	// Hold applyMu across validation and apply so a concurrent changes.create
	// (or managedZones.delete) on the same zone cannot invalidate a decision
	// this call already made — the dns driver has no multi-key transaction
	// primitive, so this lock is what makes the batch's all-or-nothing
	// semantics hold under concurrency, not just single-threaded.
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	info, err := h.dns.GetZone(r.Context(), id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Cloud DNS applies a change atomically: validate the whole batch up front —
	// deletions resolve and don't strip the apex, additions don't collide, and no
	// name is left with a CNAME beside another type — before any mutation, so a
	// bad batch fails cleanly without half-applying.
	if !h.checkDeletions(w, r, id, apexDNSName(info), &req) ||
		!h.checkAdditions(w, r, id, &req) ||
		!h.checkCNAME(w, r, id, &req) {
		return
	}

	if !h.applyChange(w, r, id, &req) {
		return
	}

	change := changeJSON{
		Kind:      kindChange,
		Additions: req.Additions,
		Deletions: req.Deletions,
		Status:    changeStatusDone,
		StartTime: time.Now().UTC().Format(time.RFC3339),
	}

	h.recordChange(id, &change)

	gcprest.WriteJSON(w, http.StatusOK, change)
}

// checkDeletions validates a change's deletions: each must resolve, and a pure
// deletion of the apex NS/SOA (one not paired with a re-adding of the same
// rrset) is rejected as Cloud DNS forbids removing them.
func (h *Handler) checkDeletions(w http.ResponseWriter, r *http.Request, id, dnsName string, req *changeJSON) bool {
	adding := make(map[string]bool, len(req.Additions))
	for i := range req.Additions {
		adding[rrsetKey(req.Additions[i].Name, req.Additions[i].Type)] = true
	}

	for i := range req.Deletions {
		d := &req.Deletions[i]
		if isApexRRSet(d.Name, d.Type, dnsName) && !adding[rrsetKey(d.Name, d.Type)] {
			gcprest.WriteError(w, http.StatusBadRequest, "invalid",
				"the resource record set at the zone apex ("+d.Type+") cannot be deleted")
			return false
		}

		rec, gerr := h.dns.GetRecord(r.Context(), id, d.Name, d.Type)
		if gerr != nil {
			gcprest.WriteCErr(w, gerr)
			return false
		}

		// Cloud DNS requires a deletion to name the record set exactly as it
		// currently stands — same TTL and same rrdatas (order-independent). A
		// mismatch is rejected 412 conditionNotMet and nothing is deleted.
		if !rrsetDeletionMatches(rec, d) {
			gcprest.WriteError(w, http.StatusPreconditionFailed, "conditionNotMet",
				"the deletion for "+d.Name+" ("+d.Type+") does not match the existing record set")
			return false
		}
	}

	return true
}

// rrsetDeletionMatches reports whether a deletion's TTL and rrdatas match the
// existing record set exactly, comparing rrdatas order-independently.
func rrsetDeletionMatches(rec *dnsdriver.RecordInfo, d *resourceRecordSetJSON) bool {
	if int64(rec.TTL) != d.TTL {
		return false
	}

	if len(rec.Values) != len(d.Rrdatas) {
		return false
	}

	got := append([]string(nil), rec.Values...)
	want := append([]string(nil), d.Rrdatas...)

	sort.Strings(got)
	sort.Strings(want)

	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

// checkAdditions validates that no addition collides with an existing record
// set. The canonical "update a record set" change deletes the old rrset and adds
// a new one with the SAME name+type in one batch; such an addition is not a real
// conflict — it replaces a record this same change removes — so exempt additions
// whose (name,type) also appears in the deletions.
func (h *Handler) checkAdditions(w http.ResponseWriter, r *http.Request, id string, req *changeJSON) bool {
	deleting := make(map[string]bool, len(req.Deletions))
	for i := range req.Deletions {
		deleting[rrsetKey(req.Deletions[i].Name, req.Deletions[i].Type)] = true
	}

	for i := range req.Additions {
		a := &req.Additions[i]

		// Validate every addition's shape before any mutation applies. The driver
		// has no batch primitive, so a malformed addition caught only at apply
		// time would land after the deletions — reject it up front so the batch
		// fails cleanly and the zone is left untouched.
		if a.Name == "" || a.Type == "" || len(a.Rrdatas) == 0 {
			gcprest.WriteError(w, http.StatusBadRequest, "invalid",
				"an addition must have a name, type, and at least one rrdata")
			return false
		}

		if deleting[rrsetKey(a.Name, a.Type)] {
			continue
		}

		if _, gerr := h.dns.GetRecord(r.Context(), id, a.Name, a.Type); gerr == nil {
			gcprest.WriteCErr(w, cerrors.Newf(cerrors.AlreadyExists,
				"record set %q %s already exists", a.Name, a.Type))
			return false
		}
	}

	return true
}

// checkCNAME rejects a change that would leave any name with a CNAME record set
// beside a record set of another type, which Cloud DNS forbids.
func (h *Handler) checkCNAME(w http.ResponseWriter, r *http.Request, id string, req *changeJSON) bool {
	current, err := h.dns.ListRecords(r.Context(), id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return false
	}

	if name, bad := cnameConflict(postApplyTypes(current, req)); bad {
		gcprest.WriteError(w, http.StatusBadRequest, "cnameResourceRecordSetConflict",
			"the resource record set at "+name+" would have a CNAME alongside another type")
		return false
	}

	return true
}

// applyChange performs the change's deletions then additions against the driver.
func (h *Handler) applyChange(w http.ResponseWriter, r *http.Request, id string, req *changeJSON) bool {
	for i := range req.Deletions {
		d := &req.Deletions[i]
		if derr := h.dns.DeleteRecord(r.Context(), id, d.Name, d.Type); derr != nil {
			gcprest.WriteCErr(w, derr)
			return false
		}
	}

	for i := range req.Additions {
		a := &req.Additions[i]
		if _, aerr := h.dns.CreateRecord(r.Context(), dnsdriver.RecordConfig{
			ZoneID: id, Name: a.Name, Type: a.Type, TTL: int(a.TTL), Values: a.Rrdatas,
		}); aerr != nil {
			gcprest.WriteCErr(w, aerr)
			return false
		}
	}

	return true
}

// postApplyTypes computes, per record-set name, the set of record types present
// after req's deletions and additions apply to the zone's current records.
func postApplyTypes(current []dnsdriver.RecordInfo, req *changeJSON) map[string]map[string]bool {
	deleting := make(map[string]bool, len(req.Deletions))
	for i := range req.Deletions {
		deleting[rrsetKey(req.Deletions[i].Name, req.Deletions[i].Type)] = true
	}

	byName := make(map[string]map[string]bool)
	add := func(name, rtype string) {
		if byName[name] == nil {
			byName[name] = make(map[string]bool)
		}

		byName[name][rtype] = true
	}

	for i := range current {
		if deleting[rrsetKey(current[i].Name, current[i].Type)] {
			continue
		}

		add(current[i].Name, current[i].Type)
	}

	for i := range req.Additions {
		add(req.Additions[i].Name, req.Additions[i].Type)
	}

	return byName
}

// cnameConflict reports the first name that would carry a CNAME record set
// alongside a record set of another type.
func cnameConflict(byName map[string]map[string]bool) (string, bool) {
	for name, types := range byName {
		if types["CNAME"] && len(types) > 1 {
			return name, true
		}
	}

	return "", false
}

// recordChange assigns the change its per-zone id (its index in the zone's
// change log) and appends it, so changes are numbered sequentially per zone
// starting at "0" — independent of managed-zone operation ids.
func (h *Handler) recordChange(zoneID string, change *changeJSON) {
	h.mu.Lock()
	defer h.mu.Unlock()

	change.ID = strconv.Itoa(len(h.changes[zoneID]))
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
