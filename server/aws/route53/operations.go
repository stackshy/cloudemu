package route53

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// listMaxItems is the default/maximum page size for record-set and zone
// listings when the caller does not request a smaller one.
const listMaxItems = 100

// TTLs for the SOA and NS records a hosted zone is seeded with on create.
const (
	soaTTL = 900
	nsTTL  = 172800
)

func (h *Handler) createHostedZone(w http.ResponseWriter, r *http.Request) {
	var req createHostedZoneRequest
	if !decodeXML(w, r, &req) {
		return
	}

	cfg := dnsdriver.ZoneConfig{
		Name:            ensureTrailingDot(req.Name),
		CallerReference: req.CallerReference,
	}
	if req.HostedZoneConfig != nil {
		cfg.Private = req.HostedZoneConfig.PrivateZone
		cfg.Comment = req.HostedZoneConfig.Comment
	}

	info, err := h.dns.CreateZone(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	// A new hosted zone starts with its authoritative SOA and NS records, just
	// like real Route 53 — so RRSetCount is 2 and downstream record management
	// (and the NS delegation the registrar needs) works.
	nameServers := nameServersFor(info.ID)
	h.seedZoneRecords(r, info.ID, info.Name, nameServers)

	// Re-read so RecordCount reflects the seeded SOA+NS records.
	if refreshed, gerr := h.dns.GetZone(r.Context(), info.ID); gerr == nil {
		info = refreshed
	}

	hz := toHostedZoneXML(info)

	// Real Route 53 returns a Location header pointing at the new zone.
	w.Header().Set("Location", pathPrefix+"/"+info.ID)

	wire.WriteXML(w, http.StatusCreated, createHostedZoneResponse{
		Xmlns:         xmlns,
		HostedZone:    hz,
		ChangeInfo:    newChangeInfo(),
		DelegationSet: delegationSetXML{NameServers: nameServers},
	})
}

// seedZoneRecords creates the SOA and NS records a new hosted zone is born with.
// Failures are ignored: seeding is best-effort convenience and must not fail the
// CreateHostedZone call itself.
func (h *Handler) seedZoneRecords(r *http.Request, zoneID, zoneName string, nameServers []string) {
	soaValue := nameServers[0] + ". awsdns-hostmaster.amazon.com. 1 7200 900 1209600 86400"

	_, _ = h.dns.CreateRecord(r.Context(), dnsdriver.RecordConfig{
		ZoneID: zoneID, Name: zoneName, Type: "SOA", TTL: soaTTL, Values: []string{soaValue},
	})
	_, _ = h.dns.CreateRecord(r.Context(), dnsdriver.RecordConfig{
		ZoneID: zoneID, Name: zoneName, Type: "NS", TTL: nsTTL, Values: nameServers,
	})
}

func (h *Handler) getHostedZone(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.dns.GetZone(r.Context(), trimZonePrefix(id))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, getHostedZoneResponse{
		Xmlns:         xmlns,
		HostedZone:    toHostedZoneXML(info),
		DelegationSet: delegationSetXML{NameServers: nameServersFor(info.ID)},
	})
}

func (h *Handler) listHostedZones(w http.ResponseWriter, r *http.Request) {
	// Route 53 hosted zones are account-global, so list them unscoped.
	infos, err := h.dns.ListZones(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	zones := make([]hostedZoneXML, 0, len(infos))
	for i := range infos {
		zones = append(zones, toHostedZoneXML(&infos[i]))
	}

	wire.WriteXML(w, http.StatusOK, listHostedZonesResponse{
		Xmlns:       xmlns,
		HostedZones: zones,
		IsTruncated: false,
		MaxItems:    listMaxItems,
	})
}

func (h *Handler) deleteHostedZone(w http.ResponseWriter, r *http.Request, id string) {
	zoneID := trimZonePrefix(id)

	zone, err := h.dns.GetZone(r.Context(), zoneID)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Real Route 53 deletes a hosted zone only when it holds nothing but the
	// default SOA and apex NS records; any other record set returns a 400
	// HostedZoneNotEmpty and deletes nothing.
	records, err := h.dns.ListRecords(r.Context(), zoneID)
	if err != nil {
		writeErr(w, err)
		return
	}

	if extra := extraRecordName(zone.Name, records); extra != "" {
		writeError(w, http.StatusBadRequest, "HostedZoneNotEmpty",
			"The hosted zone contains resource records that are not SOA or NS records, or a custom NS record: "+extra)

		return
	}

	if err := h.dns.DeleteZone(r.Context(), zoneID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, changeResourceRecordSetsResponse{
		Xmlns:      xmlns,
		ChangeInfo: newChangeInfo(),
	})
}

// extraRecordName returns the name of the first record set that blocks deletion
// (any record beyond the default apex SOA and apex NS), or "" when the zone is
// empty enough to delete. Matching is case-insensitive and trailing-dot
// insensitive so an apex name written either way is recognized.
func extraRecordName(zoneName string, records []dnsdriver.RecordInfo) string {
	apex := strings.ToLower(strings.TrimSuffix(zoneName, "."))

	for i := range records {
		rec := &records[i]
		name := strings.ToLower(strings.TrimSuffix(rec.Name, "."))
		isApex := name == apex

		if isApex && (rec.Type == "SOA" || rec.Type == "NS") {
			continue
		}

		return rec.Name
	}

	return ""
}

// changeResourceRecordSets applies a CREATE/UPSERT/DELETE batch against the
// zone. Route 53 validates the whole batch and applies nothing if any change is
// invalid (InvalidChangeBatch), so we validate every change against the zone's
// current state — tracking intra-batch effects so a DELETE followed by a CREATE
// of the same record set is allowed — before mutating anything. The batch then
// shares one INSYNC ChangeInfo for the SDK's change poller.
func (h *Handler) changeResourceRecordSets(w http.ResponseWriter, r *http.Request, id string) {
	var req changeResourceRecordSetsRequest
	if !decodeXML(w, r, &req) {
		return
	}

	zoneID := trimZonePrefix(id)

	// Route 53 stores and returns record names as FQDNs (with a trailing dot).
	// Normalize once here so validation, create, delete, and upsert all agree on
	// the stored name whether the client sent the dot or not.
	for i := range req.ChangeBatch.Changes {
		rr := &req.ChangeBatch.Changes[i].ResourceRecordSet
		rr.Name = ensureTrailingDot(rr.Name)
	}

	// The hosted zone must exist before the change batch is validated: a change
	// against a missing zone is NoSuchHostedZone (404), not the batch-level
	// InvalidChangeBatch (400). Only record-level errors from batch validation
	// map to InvalidChangeBatch.
	zone, err := h.dns.GetZone(r.Context(), zoneID)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.validateChangeBatch(r, zone, req.ChangeBatch.Changes); err != nil {
		writeChangeErr(w, err)
		return
	}

	for i := range req.ChangeBatch.Changes {
		if err := h.applyChange(r, zoneID, &req.ChangeBatch.Changes[i]); err != nil {
			writeChangeErr(w, err)
			return
		}
	}

	wire.WriteXML(w, http.StatusOK, changeResourceRecordSetsResponse{
		Xmlns:      xmlns,
		ChangeInfo: newChangeInfo(),
	})
}

// rrSetKey identifies a record set for batch validation, matching the driver's
// identity (name is case-insensitive, type is case-insensitive, set ID
// distinguishes weighted records at the same name+type).
func rrSetKey(name, rtype, setID string) string {
	return strings.ToLower(name) + "|" + strings.ToUpper(rtype) + "|" + setID
}

// validateRecordSet checks a single record set's fields are self-consistent,
// independent of the zone's current state. Real Route 53 rejects these as part
// of change-batch validation before any change is applied. apex is the zone's
// FQDN, used to reject a CNAME at the zone apex.
func validateRecordSet(rr *resourceRecordSetXML, apex string) error {
	if rr.Name == "" || rr.Type == "" {
		return cerrors.New(cerrors.InvalidArgument, "record set name and type are required")
	}

	// A CNAME is not permitted at the zone apex — the apex must carry the SOA and
	// NS records, so it can only use an A/AAAA or an ALIAS. Real Route 53 rejects
	// an apex CNAME as an InvalidChangeBatch (FailedPrecondition maps to that).
	if strings.EqualFold(rr.Type, "CNAME") && sameDNSName(rr.Name, apex) {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"RRSet of type CNAME with DNS name %s is not permitted at apex in zone %s",
			ensureTrailingDot(rr.Name), ensureTrailingDot(apex))
	}

	// Weighted routing record sets require a non-empty SetIdentifier that
	// distinguishes them from their siblings at the same name+type. Real Route 53
	// rejects a weighted record set with a missing SetIdentifier as an
	// InvalidChangeBatch (FailedPrecondition maps to that code).
	if rr.Weight != nil && rr.SetIdentifier == "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"record set %q %s: SetIdentifier is required for weighted routing", rr.Name, rr.Type)
	}

	return nil
}

// sameDNSName reports whether two DNS names are equal, case- and
// trailing-dot-insensitively.
func sameDNSName(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

// deleteMatchesRecord reports whether a DELETE request's TTL and values exactly
// match the stored record set — Route 53's rule that a DELETE must repeat the
// record's current values. Alias records match on their alias target instead of
// a TTL and resource records.
func deleteMatchesRecord(cur *dnsdriver.RecordInfo, rr *resourceRecordSetXML) bool {
	if cur.AliasTarget != nil || rr.AliasTarget != nil {
		if cur.AliasTarget == nil || rr.AliasTarget == nil {
			return false
		}

		return sameDNSName(cur.AliasTarget.DNSName, rr.AliasTarget.DNSName) &&
			cur.AliasTarget.HostedZoneID == rr.AliasTarget.HostedZoneId
	}

	reqTTL := 0
	if rr.TTL != nil {
		reqTTL = int(*rr.TTL)
	}

	if cur.TTL != reqTTL {
		return false
	}

	reqValues := make([]string, 0, len(rr.ResourceRecords))
	for _, v := range rr.ResourceRecords {
		reqValues = append(reqValues, v.Value)
	}

	return sameValueSet(cur.Values, reqValues)
}

// sameValueSet reports whether two resource-record value lists are equal as
// sets (order-independent), matching how Route 53 compares a DELETE's values.
func sameValueSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	as := append([]string(nil), a...)
	sort.Strings(as)

	bs := append([]string(nil), b...)
	sort.Strings(bs)

	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}

	return true
}

// validateChangeBatch checks every change would apply cleanly before any is
// applied, so an invalid batch is rejected whole (nothing half-applied). It
// simulates the batch against the zone's current record sets, folding in each
// change's effect so ordering within the batch is respected.
func (h *Handler) validateChangeBatch(r *http.Request, zone *dnsdriver.ZoneInfo, changes []changeItem) error {
	existing, err := h.dns.ListRecords(r.Context(), zone.ID)
	if err != nil {
		return err
	}

	present := make(map[string]bool, len(existing))
	byKey := make(map[string]*dnsdriver.RecordInfo, len(existing))
	for i := range existing {
		key := rrSetKey(existing[i].Name, existing[i].Type, existing[i].SetID)
		present[key] = true
		byKey[key] = &existing[i]
	}

	for i := range changes {
		if err := validateChange(&changes[i], zone.Name, present, byKey); err != nil {
			return err
		}
	}

	return nil
}

// validateChange checks one change against the batch's simulated presence set
// (present) and the zone's pre-batch record sets (byKey), folding the change's
// effect back into present so later changes in the batch see it.
func validateChange(
	ch *changeItem, apex string, present map[string]bool, byKey map[string]*dnsdriver.RecordInfo,
) error {
	rr := &ch.ResourceRecordSet

	if err := validateRecordSet(rr, apex); err != nil {
		return err
	}

	key := rrSetKey(rr.Name, rr.Type, rr.SetIdentifier)

	switch ch.Action {
	case actionCreate:
		if present[key] {
			return cerrors.Newf(cerrors.AlreadyExists, "record set %q %s already exists", rr.Name, rr.Type)
		}

		present[key] = true
	case actionDelete:
		if !present[key] {
			return cerrors.Newf(cerrors.NotFound, "record set %q %s not found", rr.Name, rr.Type)
		}
		// Route 53 requires a DELETE to specify the exact TTL and values of the
		// existing record set. Enforce it only against a record that was already
		// present before this batch (byKey); one created earlier in the same
		// batch has no pre-existing values to match against.
		if cur, ok := byKey[key]; ok && !deleteMatchesRecord(cur, rr) {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"Tried to delete resource record set [name=%s, type=%s] but the values provided do not match the current values",
				ensureTrailingDot(rr.Name), rr.Type)
		}

		present[key] = false
	case actionUpsert:
		present[key] = true
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported change action %q", ch.Action)
	}

	return nil
}

// applyChange executes a single record change against the driver.
func (h *Handler) applyChange(r *http.Request, zoneID string, ch *changeItem) error {
	rr := &ch.ResourceRecordSet
	cfg := recordConfig(zoneID, rr)

	switch ch.Action {
	case actionDelete:
		return h.deleteRecordSet(r, zoneID, rr)
	case actionCreate:
		_, err := h.dns.CreateRecord(r.Context(), cfg)
		return err
	case actionUpsert:
		return h.upsertRecord(r, cfg)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported change action %q", ch.Action)
	}
}

// recordSetDeleter is the AWS-only extension a Route 53 backend implements to
// delete one record set addressed by SetIdentifier, leaving weighted/latency/
// failover/geo siblings at the same name+type untouched. Backends without it
// fall back to the SetIdentifier-less DeleteRecord.
type recordSetDeleter interface {
	DeleteRecordSet(ctx context.Context, zoneID, name, recordType, setID string) error
}

// deleteRecordSet removes exactly the record set identified by name+type+
// SetIdentifier. A weighted/latency/failover/geo DELETE must not disturb its
// siblings, so the SetIdentifier is honored when the backend supports it.
func (h *Handler) deleteRecordSet(r *http.Request, zoneID string, rr *resourceRecordSetXML) error {
	if d, ok := h.dns.(recordSetDeleter); ok {
		return d.DeleteRecordSet(r.Context(), zoneID, rr.Name, rr.Type, rr.SetIdentifier)
	}

	return h.dns.DeleteRecord(r.Context(), zoneID, rr.Name, rr.Type)
}

// upsertRecord creates the record set, and on an exact-key conflict updates it
// instead — Route 53's UPSERT semantics keyed by name+type+SetIdentifier. Going
// through CreateRecord first keeps a new weighted/geo sibling from being
// misrouted into an update of a different SetIdentifier's record.
func (h *Handler) upsertRecord(r *http.Request, cfg dnsdriver.RecordConfig) error {
	_, err := h.dns.CreateRecord(r.Context(), cfg)
	switch {
	case err == nil:
		return nil
	case cerrors.IsAlreadyExists(err):
		_, uerr := h.dns.UpdateRecord(r.Context(), cfg)
		return uerr
	default:
		return err
	}
}

func (h *Handler) listResourceRecordSets(w http.ResponseWriter, r *http.Request, id string) {
	records, err := h.dns.ListRecords(r.Context(), trimZonePrefix(id))
	if err != nil {
		writeErr(w, err)
		return
	}

	// Real Route 53 returns record sets in DNS name order — sorted first by DNS
	// name with the labels reversed (so the zone apex sorts before its
	// subdomains: com.order. < com.order.a.), then by record type. This ordering
	// is what NextRecordName/StartRecordName pagination walks.
	sort.Slice(records, func(i, j int) bool {
		if c := compareDNSName(records[i].Name, records[j].Name); c != 0 {
			return c < 0
		}

		return records[i].Type < records[j].Type
	})

	// StartRecordName (and optional StartRecordType) skip forward to the first
	// record at or after the requested position, in the same reversed-label order.
	start := r.URL.Query().Get("name")
	startType := r.URL.Query().Get("type")
	records = recordsFrom(records, start, startType)

	maxItems := parseMaxItems(r.URL.Query().Get("maxitems"))

	resp := listResourceRecordSetsResponse{
		Xmlns:    xmlns,
		MaxItems: int32(maxItems),
	}

	if len(records) > maxItems {
		next := records[maxItems]
		resp.IsTruncated = true
		resp.NextRecordName = next.Name
		resp.NextRecordType = next.Type
		records = records[:maxItems]
	}

	resp.ResourceRecordSets = make([]resourceRecordSetXML, 0, len(records))
	for i := range records {
		resp.ResourceRecordSets = append(resp.ResourceRecordSets, toRecordSetXML(&records[i]))
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// recordsFrom returns the slice starting at the first record whose (name, type)
// is at or after (start, startType) in reversed-label DNS order. An empty start
// returns all records.
func recordsFrom(records []dnsdriver.RecordInfo, start, startType string) []dnsdriver.RecordInfo {
	if start == "" {
		return records
	}

	for i := range records {
		c := compareDNSName(records[i].Name, start)
		if c > 0 || (c == 0 && (startType == "" || records[i].Type >= startType)) {
			return records[i:]
		}
	}

	return nil
}

// compareDNSName orders two DNS names the way Route 53's ListResourceRecordSets
// does: by the name's labels reversed (TLD first), so a zone apex sorts before
// its subdomains. Comparison is case- and trailing-dot-insensitive. It returns
// -1, 0, or 1.
func compareDNSName(a, b string) int {
	la, lb := reversedLabels(a), reversedLabels(b)

	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			if la[i] < lb[i] {
				return -1
			}

			return 1
		}
	}

	switch {
	case len(la) < len(lb):
		return -1
	case len(la) > len(lb):
		return 1
	default:
		return 0
	}
}

// reversedLabels lower-cases a DNS name, drops the trailing dot, and returns its
// labels reversed (TLD first): "www.Order.com." → ["com", "order", "www"].
func reversedLabels(name string) []string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return nil
	}

	labels := strings.Split(name, ".")
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}

	return labels
}

// parseMaxItems reads the maxitems query param, clamping to the fixed page size
// when absent or invalid.
func parseMaxItems(v string) int {
	if v == "" {
		return listMaxItems
	}

	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > listMaxItems {
		return listMaxItems
	}

	return n
}

// ensureTrailingDot returns name as an FQDN (with a trailing dot), the form
// Route 53 stores and returns zone and record names in. An empty name is left
// as-is, and a name that already ends in a dot is unchanged.
func ensureTrailingDot(name string) string {
	if name == "" || strings.HasSuffix(name, ".") {
		return name
	}

	return name + "."
}

// recordConfig builds a driver RecordConfig from a parsed record set element.
func recordConfig(zoneID string, rr *resourceRecordSetXML) dnsdriver.RecordConfig {
	values := make([]string, 0, len(rr.ResourceRecords))
	for _, v := range rr.ResourceRecords {
		values = append(values, v.Value)
	}

	cfg := dnsdriver.RecordConfig{
		ZoneID:           zoneID,
		Name:             rr.Name,
		Type:             rr.Type,
		Values:           values,
		SetID:            rr.SetIdentifier,
		Region:           rr.Region,
		Failover:         rr.Failover,
		HealthCheckID:    rr.HealthCheckId,
		MultiValueAnswer: rr.MultiValueAnswer,
	}

	if rr.TTL != nil {
		cfg.TTL = int(*rr.TTL)
	}

	if rr.Weight != nil {
		w := int(*rr.Weight)
		cfg.Weight = &w
	}

	if rr.GeoLocation != nil {
		cfg.GeoLocation = &dnsdriver.GeoLocation{
			ContinentCode:   rr.GeoLocation.ContinentCode,
			CountryCode:     rr.GeoLocation.CountryCode,
			SubdivisionCode: rr.GeoLocation.SubdivisionCode,
		}
	}

	if rr.AliasTarget != nil {
		cfg.AliasTarget = &dnsdriver.AliasTarget{
			DNSName:              rr.AliasTarget.DNSName,
			HostedZoneID:         rr.AliasTarget.HostedZoneId,
			EvaluateTargetHealth: rr.AliasTarget.EvaluateTargetHealth,
		}
	}

	return cfg
}

// newChangeInfo returns a synthetic INSYNC ChangeInfo for a mutating op. Each
// call gets a distinct change id so two changes are distinguishable (real
// Route 53 assigns a fresh id per change); GetChange reports INSYNC for any id.
func newChangeInfo() changeInfoXML {
	return changeInfoXML{
		Id:          newChangeID(),
		Status:      changeStatusInsync,
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// cleanMsg returns a cloudemu error's message without the leading canonical
// "Code: " prefix its Error() string carries, so the wire message doesn't leak
// an internal code (e.g. "NotFound:") alongside the AWS error code element.
func cleanMsg(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		return msg[i+2:]
	}

	return msg
}

// decodeXML reads an XML request body into v, writing an InvalidInput error and
// returning false on a decode failure.
func decodeXML(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := xml.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "invalid XML: "+err.Error())
		return false
	}

	return true
}

// writeError writes a Route 53 XML error response with the given status.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	wire.WriteXML(w, status, errorResponse{
		Xmlns: xmlns,
		Error: errorXML{Code: code, Message: msg},
	})
}

// writeErr maps a canonical cloudemu error to a Route 53 XML error response.
// It is for zone-level operations (Get/Delete/CreateHostedZone), where a
// missing or duplicate resource is the zone itself.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NoSuchHostedZone", cleanMsg(err))
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "HostedZoneAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "InvalidChangeBatch", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

// writeChangeErr maps a driver error from a ChangeResourceRecordSets batch. The
// zone is known to exist here, so a missing/duplicate *record* is a bad change
// batch — real Route 53 returns InvalidChangeBatch (400), not the zone-level
// NoSuchHostedZone/HostedZoneAlreadyExists codes.
func writeChangeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err), cerrors.IsAlreadyExists(err), cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "InvalidChangeBatch", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
