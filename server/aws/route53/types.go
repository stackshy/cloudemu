package route53

import (
	"encoding/xml"
	"strings"

	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// xmlns is the Route 53 XML namespace stamped on every response root element.
const xmlns = "https://route53.amazonaws.com/doc/2013-04-01/"

// Change actions accepted in a ChangeResourceRecordSets batch.
const (
	actionCreate = "CREATE"
	actionUpsert = "UPSERT"
	actionDelete = "DELETE"
)

// changeStatusInsync is the terminal ChangeInfo status; our mock applies
// changes synchronously so every change is immediately INSYNC.
const changeStatusInsync = "INSYNC"

// --- shared record element ---

// resourceRecordXML is a single record value (<ResourceRecord><Value>…).
type resourceRecordXML struct {
	Value string `xml:"Value"`
}

// aliasTargetXML is the Route 53 AliasTarget element (an A/AAAA record that
// points at another AWS resource instead of carrying a TTL + ResourceRecords).
type aliasTargetXML struct {
	HostedZoneId         string `xml:"HostedZoneId"`
	DNSName              string `xml:"DNSName"`
	EvaluateTargetHealth bool   `xml:"EvaluateTargetHealth"`
}

// geoLocationXML is the Route 53 GeoLocation element for geolocation routing.
type geoLocationXML struct {
	ContinentCode   string `xml:"ContinentCode,omitempty"`
	CountryCode     string `xml:"CountryCode,omitempty"`
	SubdivisionCode string `xml:"SubdivisionCode,omitempty"`
}

// resourceRecordSetXML is the Route 53 ResourceRecordSet element.
type resourceRecordSetXML struct {
	Name             string              `xml:"Name"`
	Type             string              `xml:"Type"`
	SetIdentifier    string              `xml:"SetIdentifier,omitempty"`
	Weight           *int64              `xml:"Weight,omitempty"`
	Region           string              `xml:"Region,omitempty"`
	Failover         string              `xml:"Failover,omitempty"`
	GeoLocation      *geoLocationXML     `xml:"GeoLocation,omitempty"`
	MultiValueAnswer *bool               `xml:"MultiValueAnswer,omitempty"`
	HealthCheckId    string              `xml:"HealthCheckId,omitempty"`
	TTL              *int64              `xml:"TTL,omitempty"`
	ResourceRecords  []resourceRecordXML `xml:"ResourceRecords>ResourceRecord,omitempty"`
	AliasTarget      *aliasTargetXML     `xml:"AliasTarget,omitempty"`
}

// --- hosted zone elements ---

type hostedZoneConfigXML struct {
	Comment     string `xml:"Comment,omitempty"`
	PrivateZone bool   `xml:"PrivateZone"`
}

// hostedZoneXML is the Route 53 HostedZone element. Id is returned in the
// "/hostedzone/{id}" form real Route 53 uses; the SDK accepts either form back
// as input.
type hostedZoneXML struct {
	Id                     string               `xml:"Id"`
	Name                   string               `xml:"Name"`
	CallerReference        string               `xml:"CallerReference"`
	Config                 *hostedZoneConfigXML `xml:"Config,omitempty"`
	ResourceRecordSetCount int64                `xml:"ResourceRecordSetCount"`
}

type changeInfoXML struct {
	Id          string `xml:"Id"`
	Status      string `xml:"Status"`
	SubmittedAt string `xml:"SubmittedAt"`
}

// delegationSetXML is the Route 53 DelegationSet element carrying the four
// authoritative name servers a registrar must be pointed at.
type delegationSetXML struct {
	NameServers []string `xml:"NameServers>NameServer"`
}

// --- request envelopes ---

type createHostedZoneRequest struct {
	XMLName          xml.Name             `xml:"CreateHostedZoneRequest"`
	Name             string               `xml:"Name"`
	CallerReference  string               `xml:"CallerReference"`
	HostedZoneConfig *hostedZoneConfigXML `xml:"HostedZoneConfig"`
}

type changeResourceRecordSetsRequest struct {
	XMLName     xml.Name    `xml:"ChangeResourceRecordSetsRequest"`
	ChangeBatch changeBatch `xml:"ChangeBatch"`
}

type changeBatch struct {
	Comment string       `xml:"Comment,omitempty"`
	Changes []changeItem `xml:"Changes>Change"`
}

type changeItem struct {
	Action            string               `xml:"Action"`
	ResourceRecordSet resourceRecordSetXML `xml:"ResourceRecordSet"`
}

// --- response envelopes ---

type createHostedZoneResponse struct {
	XMLName       xml.Name         `xml:"CreateHostedZoneResponse"`
	Xmlns         string           `xml:"xmlns,attr"`
	HostedZone    hostedZoneXML    `xml:"HostedZone"`
	ChangeInfo    changeInfoXML    `xml:"ChangeInfo"`
	DelegationSet delegationSetXML `xml:"DelegationSet"`
}

type getHostedZoneResponse struct {
	XMLName       xml.Name         `xml:"GetHostedZoneResponse"`
	Xmlns         string           `xml:"xmlns,attr"`
	HostedZone    hostedZoneXML    `xml:"HostedZone"`
	DelegationSet delegationSetXML `xml:"DelegationSet"`
}

type getChangeResponse struct {
	XMLName    xml.Name      `xml:"GetChangeResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo changeInfoXML `xml:"ChangeInfo"`
}

type getHostedZoneCountResponse struct {
	XMLName         xml.Name `xml:"GetHostedZoneCountResponse"`
	Xmlns           string   `xml:"xmlns,attr"`
	HostedZoneCount int64    `xml:"HostedZoneCount"`
}

type listHostedZonesByNameResponse struct {
	XMLName          xml.Name        `xml:"ListHostedZonesByNameResponse"`
	Xmlns            string          `xml:"xmlns,attr"`
	HostedZones      []hostedZoneXML `xml:"HostedZones>HostedZone"`
	DNSName          string          `xml:"DNSName,omitempty"`
	IsTruncated      bool            `xml:"IsTruncated"`
	NextDNSName      string          `xml:"NextDNSName,omitempty"`
	NextHostedZoneId string          `xml:"NextHostedZoneId,omitempty"`
	MaxItems         int32           `xml:"MaxItems"`
}

type testDNSAnswerResponse struct {
	XMLName      xml.Name `xml:"TestDNSAnswerResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	Nameserver   string   `xml:"Nameserver"`
	RecordName   string   `xml:"RecordName"`
	RecordType   string   `xml:"RecordType"`
	RecordData   []string `xml:"RecordData>RecordDataEntry"`
	ResponseCode string   `xml:"ResponseCode"`
	Protocol     string   `xml:"Protocol"`
}

type listHostedZonesResponse struct {
	XMLName     xml.Name        `xml:"ListHostedZonesResponse"`
	Xmlns       string          `xml:"xmlns,attr"`
	HostedZones []hostedZoneXML `xml:"HostedZones>HostedZone"`
	IsTruncated bool            `xml:"IsTruncated"`
	MaxItems    int32           `xml:"MaxItems"`
}

type changeResourceRecordSetsResponse struct {
	XMLName    xml.Name      `xml:"ChangeResourceRecordSetsResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo changeInfoXML `xml:"ChangeInfo"`
}

type listResourceRecordSetsResponse struct {
	XMLName            xml.Name               `xml:"ListResourceRecordSetsResponse"`
	Xmlns              string                 `xml:"xmlns,attr"`
	ResourceRecordSets []resourceRecordSetXML `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated        bool                   `xml:"IsTruncated"`
	NextRecordName     string                 `xml:"NextRecordName,omitempty"`
	NextRecordType     string                 `xml:"NextRecordType,omitempty"`
	MaxItems           int32                  `xml:"MaxItems"`
}

// errorResponse is the Route 53 XML error body the SDK maps to a typed
// exception via its <Error><Code> element.
type errorResponse struct {
	XMLName xml.Name `xml:"ErrorResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Error   errorXML `xml:"Error"`
}

type errorXML struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// trimZonePrefix strips the "/hostedzone/" prefix real Route 53 wraps zone ids
// in, so the driver id can be recovered whichever form the SDK echoes back on
// a subsequent request path.
func trimZonePrefix(id string) string {
	id = strings.TrimPrefix(id, "/")
	id = strings.TrimPrefix(id, "hostedzone/")

	return id
}

// toHostedZoneXML converts a driver zone into its Route 53 element. The zone id
// is returned bare (not "/hostedzone/{id}"): the SDK binds the id straight into
// the request URL path with no prefix stripping, so echoing the bare driver id
// keeps Get/Delete round-trips addressing the same resource.
//
// CallerReference is the caller-supplied idempotency token, persisted on create
// and returned verbatim here so Get/List round-trip faithfully.
func toHostedZoneXML(info *dnsdriver.ZoneInfo) hostedZoneXML {
	return hostedZoneXML{
		Id:              info.ID,
		Name:            info.Name,
		CallerReference: info.CallerReference,
		Config: &hostedZoneConfigXML{
			PrivateZone: info.Private,
		},
		ResourceRecordSetCount: int64(info.RecordCount),
	}
}

// toRecordSetXML converts a driver record into its Route 53 element. An alias
// record carries an AliasTarget instead of a TTL and ResourceRecords, so those
// are omitted when AliasTarget is present (matching real Route 53).
func toRecordSetXML(rec *dnsdriver.RecordInfo) resourceRecordSetXML {
	out := resourceRecordSetXML{
		Name:             rec.Name,
		Type:             rec.Type,
		SetIdentifier:    rec.SetID,
		Region:           rec.Region,
		Failover:         rec.Failover,
		HealthCheckId:    rec.HealthCheckID,
		MultiValueAnswer: rec.MultiValueAnswer,
	}

	if rec.AliasTarget != nil {
		out.AliasTarget = &aliasTargetXML{
			HostedZoneId:         rec.AliasTarget.HostedZoneID,
			DNSName:              rec.AliasTarget.DNSName,
			EvaluateTargetHealth: rec.AliasTarget.EvaluateTargetHealth,
		}
	} else {
		ttl := int64(rec.TTL)
		out.TTL = &ttl

		rrs := make([]resourceRecordXML, 0, len(rec.Values))
		for _, v := range rec.Values {
			rrs = append(rrs, resourceRecordXML{Value: v})
		}

		out.ResourceRecords = rrs
	}

	if rec.Weight != nil {
		w := int64(*rec.Weight)
		out.Weight = &w
	}

	if rec.GeoLocation != nil {
		out.GeoLocation = &geoLocationXML{
			ContinentCode:   rec.GeoLocation.ContinentCode,
			CountryCode:     rec.GeoLocation.CountryCode,
			SubdivisionCode: rec.GeoLocation.SubdivisionCode,
		}
	}

	return out
}
