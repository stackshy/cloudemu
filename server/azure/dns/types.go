package dns

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// ARM resource type strings stamped on responses.
const (
	zoneResourceType    = "Microsoft.Network/dnsZones"
	recordSetTypePrefix = "Microsoft.Network/dnsZones/"
	defaultZoneLocation = "global"
	defaultRecordTTL    = 3600

	// Canonical DNS record-type strings, keyed on the upper-cased URL segment.
	recTypeA     = "A"
	recTypeAAAA  = "AAAA"
	recTypeCNAME = "CNAME"
	recTypeTXT   = "TXT"
	recTypeNS    = "NS"
	recTypePTR   = "PTR"
	recTypeSOA   = "SOA"

	// maxNumberOfRecordSets is the fixed per-zone record-set cap Azure DNS
	// reports for a public zone (a read-only property).
	maxNumberOfRecordSets = 10000
	// maxNumberOfRecordsPerRecordSet is the fixed per-record-set record cap
	// Azure DNS reports (read-only).
	maxNumberOfRecordsPerRecordSet = 20

	// apexRecordName is the relative name Azure uses for a zone's apex records
	// (the auto-created SOA and NS record sets).
	apexRecordName = "@"
	// nsRecordTTL and soaRecordTTL are the TTLs Azure assigns the auto-created
	// apex NS and SOA record sets.
	nsRecordTTL  = 172800
	soaRecordTTL = 3600

	// nameServerShardCount bounds the 1-based shard embedded in a zone's name
	// servers (ns1-NN...).
	nameServerShardCount = 99

	// headerIfMatch and headerIfNoneMatch are the conditional-request headers
	// the armdns SDK sets from RecordSetsClientCreateOrUpdateOptions.IfMatch /
	// IfNoneMatch, carrying a record set's optimistic-concurrency precondition.
	headerIfMatch     = "If-Match"
	headerIfNoneMatch = "If-None-Match"

	// SOA record defaults Azure stamps on the auto-created apex SOA. host and
	// email are stored per-zone (host = the zone's first name server); the
	// timing fields are fixed platform defaults.
	soaEmail       = "azuredns-hostmaster.microsoft.com"
	soaRefreshTime = 3600
	soaRetryTime   = 300
	soaExpireTime  = 2419200
	soaMinimumTTL  = 300
	soaSerial      = 1
)

// nameServerSuffixes are the four authoritative name-server domains Azure DNS
// delegates a public zone to (ns1-NN.azure-dns.com / .net / .org / .info).
//
//nolint:gochecknoglobals // fixed platform constant, read-only lookup table
var nameServerSuffixes = [...]string{"azure-dns.com", "azure-dns.net", "azure-dns.org", "azure-dns.info"}

// zoneNameServers returns the four authoritative name servers Azure assigns a
// public zone. Private zones have no name servers. The "NN" shard is derived
// deterministically from the zone name so a zone always reports the same set.
func zoneNameServers(zoneName string, private bool) []string {
	if private {
		return nil
	}

	shard := nameServerShard(zoneName)

	out := make([]string, len(nameServerSuffixes))
	for i, suffix := range nameServerSuffixes {
		out[i] = fmt.Sprintf("ns%d-%02d.%s", i+1, shard, suffix)
	}

	return out
}

// nameServerShard maps a zone name to the 1-99 shard Azure embeds in its name
// servers (ns1-NN...). Deterministic so repeated reads are stable.
func nameServerShard(zoneName string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(zoneName))

	return int(h.Sum32()%nameServerShardCount) + 1
}

// --- zone JSON ---

type zoneProperties struct {
	ZoneType                       string   `json:"zoneType,omitempty"`
	MaxNumberOfRecordSets          int64    `json:"maxNumberOfRecordSets,omitempty"`
	MaxNumberOfRecordsPerRecordSet int64    `json:"maxNumberOfRecordsPerRecordSet,omitempty"`
	NumberOfRecordSets             int64    `json:"numberOfRecordSets,omitempty"`
	NameServers                    []string `json:"nameServers,omitempty"`
}

type zoneJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Etag       string            `json:"etag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *zoneProperties   `json:"properties,omitempty"`
}

type zoneListResult struct {
	Value []zoneJSON `json:"value"`
}

// --- record-set JSON ---
//
// Azure models record data as per-type nested arrays. We carry every element
// type the driver's string values can populate and convert both directions in
// recordConfig / toRecordSetJSON.

type aRecordJSON struct {
	IPv4Address string `json:"ipv4Address,omitempty"`
}

type aaaaRecordJSON struct {
	IPv6Address string `json:"ipv6Address,omitempty"`
}

type cnameRecordJSON struct {
	Cname string `json:"cname,omitempty"`
}

type txtRecordJSON struct {
	Value []string `json:"value,omitempty"`
}

// txtChunkSize is the maximum length of a single DNS TXT character-string.
// Azure represents a TXT record as an array of these ≤255-byte chunks.
const txtChunkSize = 255

// chunkTXT splits a logical TXT value into ≤255-byte character-strings, as the
// TXT wire format (and Azure's TxtRecords value array) requires. A value that
// already fits returns a single chunk; the input side rejoins them.
func chunkTXT(s string) []string {
	if len(s) <= txtChunkSize {
		return []string{s}
	}

	chunks := make([]string, 0, len(s)/txtChunkSize+1)
	for len(s) > txtChunkSize {
		chunks = append(chunks, s[:txtChunkSize])
		s = s[txtChunkSize:]
	}
	if len(s) > 0 {
		chunks = append(chunks, s)
	}

	return chunks
}

type nsRecordJSON struct {
	Nsdname string `json:"nsdname,omitempty"`
}

type ptrRecordJSON struct {
	Ptrdname string `json:"ptrdname,omitempty"`
}

type soaRecordJSON struct {
	Host         string `json:"host,omitempty"`
	Email        string `json:"email,omitempty"`
	SerialNumber int64  `json:"serialNumber,omitempty"`
	RefreshTime  int64  `json:"refreshTime,omitempty"`
	RetryTime    int64  `json:"retryTime,omitempty"`
	ExpireTime   int64  `json:"expireTime,omitempty"`
	MinimumTTL   int64  `json:"minimumTTL,omitempty"`
}

type recordSetProperties struct {
	TTL         *int64           `json:"TTL,omitempty"`
	Fqdn        string           `json:"fqdn,omitempty"`
	ARecords    []aRecordJSON    `json:"ARecords,omitempty"`
	AaaaRecords []aaaaRecordJSON `json:"AAAARecords,omitempty"`
	CnameRecord *cnameRecordJSON `json:"CNAMERecord,omitempty"`
	TxtRecords  []txtRecordJSON  `json:"TXTRecords,omitempty"`
	NsRecords   []nsRecordJSON   `json:"NSRecords,omitempty"`
	PtrRecords  []ptrRecordJSON  `json:"PTRRecords,omitempty"`
	SoaRecord   *soaRecordJSON   `json:"SOARecord,omitempty"`
}

type recordSetJSON struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Etag       string               `json:"etag,omitempty"`
	Properties *recordSetProperties `json:"properties,omitempty"`
}

type recordSetListResult struct {
	Value []recordSetJSON `json:"value"`
}

// zoneType maps the driver's private flag to Azure's ZoneType enum.
func zoneType(private bool) string {
	if private {
		return "Private"
	}

	return "Public"
}

func privateFromZoneType(zt string) bool {
	return strings.EqualFold(zt, "Private")
}

// toZoneJSON converts a driver zone into its ARM element for the given path
// scope. Azure DNS zones are always "global" location.
func toZoneJSON(rp *azurearm.ResourcePath, info *dnsdriver.ZoneInfo) zoneJSON {
	// Build the id from the zone's own group, not the request path's — which is
	// empty on a subscription-scoped list — so the id carries its true
	// resourceGroups/{rg} segment.
	rg := info.Scope.ResourceGroup
	if rg == "" {
		rg = rp.ResourceGroup
	}

	id := azurearm.BuildResourceID(rp.Subscription, rg, providerName, typeZones, info.Name)

	return zoneJSON{
		ID:       id,
		Name:     info.Name,
		Type:     zoneResourceType,
		Location: defaultZoneLocation,
		Etag:     azurearm.ETag(id),
		Tags:     info.Tags,
		Properties: &zoneProperties{
			ZoneType:                       zoneType(info.Private),
			MaxNumberOfRecordSets:          maxNumberOfRecordSets,
			MaxNumberOfRecordsPerRecordSet: maxNumberOfRecordsPerRecordSet,
			NumberOfRecordSets:             int64(info.RecordCount),
			NameServers:                    zoneNameServers(info.Name, info.Private),
		},
	}
}

// recordValues extracts the driver's flat string values from a typed Azure
// record-set body, keyed on the record type in the URL.
func recordValues(recordType string, props *recordSetProperties) []string {
	if props == nil {
		return nil
	}

	switch strings.ToUpper(recordType) {
	case recTypeA:
		return mapStrings(props.ARecords, func(a aRecordJSON) string { return a.IPv4Address })
	case recTypeAAAA:
		return mapStrings(props.AaaaRecords, func(a aaaaRecordJSON) string { return a.IPv6Address })
	case recTypeCNAME:
		if props.CnameRecord != nil && props.CnameRecord.Cname != "" {
			return []string{props.CnameRecord.Cname}
		}
	case recTypeTXT:
		var out []string
		for _, t := range props.TxtRecords {
			out = append(out, strings.Join(t.Value, ""))
		}

		return out
	case recTypeNS:
		return mapStrings(props.NsRecords, func(n nsRecordJSON) string { return n.Nsdname })
	case recTypePTR:
		return mapStrings(props.PtrRecords, func(p ptrRecordJSON) string { return p.Ptrdname })
	case recTypeSOA:
		if props.SoaRecord != nil {
			return []string{props.SoaRecord.Host, props.SoaRecord.Email}
		}
	}

	return nil
}

// toRecordSetProperties builds the typed Azure record-set body from a driver
// record's flat string values, keyed on the record type.
func toRecordSetProperties(rec *dnsdriver.RecordInfo) *recordSetProperties {
	ttl := int64(rec.TTL)
	props := &recordSetProperties{TTL: &ttl}

	switch strings.ToUpper(rec.Type) {
	case recTypeA:
		for _, v := range rec.Values {
			props.ARecords = append(props.ARecords, aRecordJSON{IPv4Address: v})
		}
	case recTypeAAAA:
		for _, v := range rec.Values {
			props.AaaaRecords = append(props.AaaaRecords, aaaaRecordJSON{IPv6Address: v})
		}
	case recTypeCNAME:
		if len(rec.Values) > 0 {
			props.CnameRecord = &cnameRecordJSON{Cname: rec.Values[0]}
		}
	case recTypeTXT:
		for _, v := range rec.Values {
			props.TxtRecords = append(props.TxtRecords, txtRecordJSON{Value: chunkTXT(v)})
		}
	case recTypeNS:
		for _, v := range rec.Values {
			props.NsRecords = append(props.NsRecords, nsRecordJSON{Nsdname: v})
		}
	case recTypePTR:
		for _, v := range rec.Values {
			props.PtrRecords = append(props.PtrRecords, ptrRecordJSON{Ptrdname: v})
		}
	case recTypeSOA:
		props.SoaRecord = toSOARecord(rec)
	}

	return props
}

// toSOARecord rebuilds the Azure SOA body from a driver record. host and email
// come from the flat values ([host, email]); the timing fields start at the
// platform defaults Azure stamps on the auto-created apex SOA and are overridden
// by any caller-edited field carried on rec.SOA, so a user-edited SOA reads back
// the edited values while an unedited one keeps Azure's defaults.
func toSOARecord(rec *dnsdriver.RecordInfo) *soaRecordJSON {
	soa := &soaRecordJSON{
		Email:        soaEmail,
		SerialNumber: soaSerial,
		RefreshTime:  soaRefreshTime,
		RetryTime:    soaRetryTime,
		ExpireTime:   soaExpireTime,
		MinimumTTL:   soaMinimumTTL,
	}

	if len(rec.Values) > 0 {
		soa.Host = rec.Values[0]
	}

	if len(rec.Values) > 1 && rec.Values[1] != "" {
		soa.Email = rec.Values[1]
	}

	applyEditedSOA(soa, rec.SOA)

	return soa
}

// applyEditedSOA overrides the SOA body's editable timing/serial/email fields
// with any non-zero value the caller edited (carried on the driver's SOA
// carrier). host is read-only and never touched here.
func applyEditedSOA(soa *soaRecordJSON, edited *dnsdriver.SOARecord) {
	if edited == nil {
		return
	}

	if edited.Email != "" {
		soa.Email = edited.Email
	}

	if edited.SerialNumber != 0 {
		soa.SerialNumber = edited.SerialNumber
	}

	if edited.RefreshTime != 0 {
		soa.RefreshTime = edited.RefreshTime
	}

	if edited.RetryTime != 0 {
		soa.RetryTime = edited.RetryTime
	}

	if edited.ExpireTime != 0 {
		soa.ExpireTime = edited.ExpireTime
	}

	if edited.MinimumTTL != 0 {
		soa.MinimumTTL = edited.MinimumTTL
	}
}

// soaConfigFromProps extracts the editable SOA timing fields from a request
// body's SOA record into the driver's typed carrier, or nil when the body
// carries no SOA record. host stays in the flat values (read-only).
func soaConfigFromProps(props *recordSetProperties) *dnsdriver.SOARecord {
	if props == nil || props.SoaRecord == nil {
		return nil
	}

	s := props.SoaRecord

	return &dnsdriver.SOARecord{
		Email:        s.Email,
		SerialNumber: s.SerialNumber,
		RefreshTime:  s.RefreshTime,
		RetryTime:    s.RetryTime,
		ExpireTime:   s.ExpireTime,
		MinimumTTL:   s.MinimumTTL,
	}
}

// mergeSOAValues overlays a PATCH's supplied SOA host/email onto the record's
// existing flat values ([host, email]), preserving the existing (system-managed)
// host and email when the PATCH omits them. Azure keeps the SOA host stable on a
// timing-only update, so a PATCH that carries no host must never blank it.
func mergeSOAValues(existing []string, props *recordSetProperties) []string {
	out := []string{"", ""}
	if len(existing) > 0 {
		out[0] = existing[0]
	}

	if len(existing) > 1 {
		out[1] = existing[1]
	}

	if props == nil || props.SoaRecord == nil {
		return out
	}

	if props.SoaRecord.Host != "" {
		out[0] = props.SoaRecord.Host
	}

	if props.SoaRecord.Email != "" {
		out[1] = props.SoaRecord.Email
	}

	return out
}

// mergeSOAConfig overlays a PATCH's supplied SOA fields onto the stored carrier,
// preserving any field the PATCH left unset (zero). Used by the record-set PATCH
// merge so a partial SOA edit keeps the fields it did not resend.
func mergeSOAConfig(base, patch *dnsdriver.SOARecord) *dnsdriver.SOARecord {
	if patch == nil {
		return base
	}

	if base == nil {
		return patch
	}

	out := *base

	if patch.Email != "" {
		out.Email = patch.Email
	}

	if patch.SerialNumber != 0 {
		out.SerialNumber = patch.SerialNumber
	}

	if patch.RefreshTime != 0 {
		out.RefreshTime = patch.RefreshTime
	}

	if patch.RetryTime != 0 {
		out.RetryTime = patch.RetryTime
	}

	if patch.ExpireTime != 0 {
		out.ExpireTime = patch.ExpireTime
	}

	if patch.MinimumTTL != 0 {
		out.MinimumTTL = patch.MinimumTTL
	}

	return &out
}

// toRecordSetJSON converts a driver record into its ARM element.
func toRecordSetJSON(rp *azurearm.ResourcePath, zone string, rec *dnsdriver.RecordInfo) recordSetJSON {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeZones, zone) +
		"/" + rec.Type + "/" + rec.Name

	props := toRecordSetProperties(rec)
	props.Fqdn = recordFqdn(rec.Name, zone)

	// A record set written since this pass always carries a driver-minted ETag
	// that rotates on every create/update. Records restored from a snapshot
	// taken before ETag support existed leave it empty; fall back to the old
	// deterministic hash so they still read back a stable (if non-rotating)
	// etag rather than an empty one.
	etag := rec.ETag
	if etag == "" {
		etag = azurearm.ETag(id)
	}

	return recordSetJSON{
		ID:         id,
		Name:       rec.Name,
		Type:       recordSetTypePrefix + rec.Type,
		Etag:       etag,
		Properties: props,
	}
}

// recordFqdn builds the fully-qualified domain name Azure reports for a record
// set: "<name>.<zone>" for a relative record, and "<zone>" for the apex ("@")
// record. Azure DNS returns the fqdn without a trailing dot (mirroring the
// zone's nameServers), so none is appended.
func recordFqdn(name, zone string) string {
	if name == "" || name == apexRecordName {
		return zone
	}

	return name + "." + zone
}

// mapStrings projects a typed slice to its string values, dropping empties.
func mapStrings[T any](in []T, f func(T) string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s := f(v); s != "" {
			out = append(out, s)
		}
	}

	return out
}

// ttlOrDefault reads the TTL from a record-set body, falling back to the DNS
// default when absent.
func ttlOrDefault(props *recordSetProperties) int {
	if props != nil && props.TTL != nil {
		return int(*props.TTL)
	}

	return defaultRecordTTL
}

// recordTypeSegment normalizes the {recordType} URL segment to the canonical
// upper-case type the driver stores.
func recordTypeSegment(s string) string {
	return strings.ToUpper(s)
}

// isApexProtectedRecord reports whether the record set is the apex SOA or NS
// that Azure DNS auto-provisions with every zone and forbids deleting. name is
// the relative record name from the URL ("@" for the apex); recordType is the
// canonical upper-case type.
func isApexProtectedRecord(name, recordType string) bool {
	if name != apexRecordName {
		return false
	}

	return recordType == recTypeSOA || recordType == recTypeNS
}

// resolveZoneID maps the SDK-facing zone name to the driver's internal zone id
// by scanning the zone list, scoped to the request's subscription and resource
// group. Scoping matters because the same zone name can exist in more than one
// resource group — a name-only scan could resolve to a zone in a different
// group. Returns a NotFound error if no such zone exists in this scope.
func (h *Handler) resolveZoneID(ctx context.Context, rp *azurearm.ResourcePath) (string, error) {
	filter := scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup}

	zones, err := h.dns.ListZones(ctx, filter)
	if err != nil {
		return "", err
	}

	// Azure treats DNS zone names case-insensitively (and lowercases them on
	// some URL paths), so match without regard to case.
	for i := range zones {
		if strings.EqualFold(zones[i].Name, rp.ResourceName) {
			return zones[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "dns zone %q not found", rp.ResourceName)
}
