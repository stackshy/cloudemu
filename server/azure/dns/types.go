package dns

import (
	"context"
	"strings"

	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// ARM resource type strings stamped on responses.
const (
	zoneResourceType    = "Microsoft.Network/dnsZones"
	recordSetTypePrefix = "Microsoft.Network/dnsZones/"
	defaultZoneLocation = "global"
	defaultRecordTTL    = 3600
)

// --- zone JSON ---

type zoneProperties struct {
	ZoneType           string   `json:"zoneType,omitempty"`
	NumberOfRecordSets int64    `json:"numberOfRecordSets,omitempty"`
	NameServers        []string `json:"nameServers,omitempty"`
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

type nsRecordJSON struct {
	Nsdname string `json:"nsdname,omitempty"`
}

type ptrRecordJSON struct {
	Ptrdname string `json:"ptrdname,omitempty"`
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
	return zoneJSON{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeZones, info.Name),
		Name:     info.Name,
		Type:     zoneResourceType,
		Location: defaultZoneLocation,
		Tags:     info.Tags,
		Properties: &zoneProperties{
			ZoneType:           zoneType(info.Private),
			NumberOfRecordSets: int64(info.RecordCount),
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
	case "A":
		return mapStrings(props.ARecords, func(a aRecordJSON) string { return a.IPv4Address })
	case "AAAA":
		return mapStrings(props.AaaaRecords, func(a aaaaRecordJSON) string { return a.IPv6Address })
	case "CNAME":
		if props.CnameRecord != nil && props.CnameRecord.Cname != "" {
			return []string{props.CnameRecord.Cname}
		}
	case "TXT":
		var out []string
		for _, t := range props.TxtRecords {
			out = append(out, strings.Join(t.Value, ""))
		}

		return out
	case "NS":
		return mapStrings(props.NsRecords, func(n nsRecordJSON) string { return n.Nsdname })
	case "PTR":
		return mapStrings(props.PtrRecords, func(p ptrRecordJSON) string { return p.Ptrdname })
	}

	return nil
}

// toRecordSetProperties builds the typed Azure record-set body from a driver
// record's flat string values, keyed on the record type.
func toRecordSetProperties(rec *dnsdriver.RecordInfo) *recordSetProperties {
	ttl := int64(rec.TTL)
	props := &recordSetProperties{TTL: &ttl}

	switch strings.ToUpper(rec.Type) {
	case "A":
		for _, v := range rec.Values {
			props.ARecords = append(props.ARecords, aRecordJSON{IPv4Address: v})
		}
	case "AAAA":
		for _, v := range rec.Values {
			props.AaaaRecords = append(props.AaaaRecords, aaaaRecordJSON{IPv6Address: v})
		}
	case "CNAME":
		if len(rec.Values) > 0 {
			props.CnameRecord = &cnameRecordJSON{Cname: rec.Values[0]}
		}
	case "TXT":
		for _, v := range rec.Values {
			props.TxtRecords = append(props.TxtRecords, txtRecordJSON{Value: []string{v}})
		}
	case "NS":
		for _, v := range rec.Values {
			props.NsRecords = append(props.NsRecords, nsRecordJSON{Nsdname: v})
		}
	case "PTR":
		for _, v := range rec.Values {
			props.PtrRecords = append(props.PtrRecords, ptrRecordJSON{Ptrdname: v})
		}
	}

	return props
}

// toRecordSetJSON converts a driver record into its ARM element.
func toRecordSetJSON(rp *azurearm.ResourcePath, zone string, rec *dnsdriver.RecordInfo) recordSetJSON {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeZones, zone) +
		"/" + rec.Type + "/" + rec.Name

	return recordSetJSON{
		ID:         id,
		Name:       rec.Name,
		Type:       recordSetTypePrefix + rec.Type,
		Properties: toRecordSetProperties(rec),
	}
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

// resolveZoneID maps the SDK-facing zone name to the driver's internal zone id
// by scanning the zone list. Returns a NotFound error if no zone with that name
// exists.
func (h *Handler) resolveZoneID(ctx context.Context, name string) (string, error) {
	zones, err := h.dns.ListZones(ctx)
	if err != nil {
		return "", err
	}

	// Azure treats DNS zone names case-insensitively (and lowercases them on
	// some URL paths), so match without regard to case.
	for i := range zones {
		if strings.EqualFold(zones[i].Name, name) {
			return zones[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "dns zone %q not found", name)
}
