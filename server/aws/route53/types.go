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

// vpcXML is the Route 53 VPC element identifying an Amazon VPC associated with a
// private hosted zone.
type vpcXML struct {
	VPCRegion string `xml:"VPCRegion"`
	VPCId     string `xml:"VPCId"`
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
	VPC              *vpcXML              `xml:"VPC"`
}

// associateVPCRequest is the AssociateVPCWithHostedZone request body.
type associateVPCRequest struct {
	XMLName xml.Name `xml:"AssociateVPCWithHostedZoneRequest"`
	Comment string   `xml:"Comment"`
	VPC     vpcXML   `xml:"VPC"`
}

// disassociateVPCRequest is the DisassociateVPCFromHostedZone request body.
type disassociateVPCRequest struct {
	XMLName xml.Name `xml:"DisassociateVPCFromHostedZoneRequest"`
	Comment string   `xml:"Comment"`
	VPC     vpcXML   `xml:"VPC"`
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
	// VPC is returned only for a private hosted zone created with a VPC.
	VPC *vpcXML `xml:"VPC,omitempty"`
}

type getHostedZoneResponse struct {
	XMLName       xml.Name         `xml:"GetHostedZoneResponse"`
	Xmlns         string           `xml:"xmlns,attr"`
	HostedZone    hostedZoneXML    `xml:"HostedZone"`
	DelegationSet delegationSetXML `xml:"DelegationSet"`
	// VPCs lists the Amazon VPCs a private hosted zone is associated with; empty
	// for a public zone.
	VPCs []vpcXML `xml:"VPCs>VPC,omitempty"`
}

// associateVPCResponse / disassociateVPCResponse each carry the ChangeInfo the
// SDK returns from an (Dis)AssociateVPCWithHostedZone call.
type associateVPCResponse struct {
	XMLName    xml.Name      `xml:"AssociateVPCWithHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo changeInfoXML `xml:"ChangeInfo"`
}

type disassociateVPCResponse struct {
	XMLName    xml.Name      `xml:"DisassociateVPCFromHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo changeInfoXML `xml:"ChangeInfo"`
}

// hostedZoneOwnerXML identifies who owns a hosted zone in a ListHostedZonesByVPC
// summary. Zones created through this API are owned by the account.
type hostedZoneOwnerXML struct {
	OwningAccount string `xml:"OwningAccount,omitempty"`
	OwningService string `xml:"OwningService,omitempty"`
}

// hostedZoneSummaryXML is one entry in a ListHostedZonesByVPC response.
type hostedZoneSummaryXML struct {
	HostedZoneId string             `xml:"HostedZoneId"`
	Name         string             `xml:"Name"`
	Owner        hostedZoneOwnerXML `xml:"Owner"`
}

type listHostedZonesByVPCResponse struct {
	XMLName             xml.Name               `xml:"ListHostedZonesByVPCResponse"`
	Xmlns               string                 `xml:"xmlns,attr"`
	HostedZoneSummaries []hostedZoneSummaryXML `xml:"HostedZoneSummaries>HostedZoneSummary"`
	MaxItems            string                 `xml:"MaxItems"`
	NextToken           string                 `xml:"NextToken,omitempty"`
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
	// Marker echoes the request marker (the id listing resumed from), empty on the
	// first page. NextMarker is present only on a truncated page and carries the id
	// the caller passes back as Marker to fetch the next page.
	Marker      string `xml:"Marker,omitempty"`
	IsTruncated bool   `xml:"IsTruncated"`
	NextMarker  string `xml:"NextMarker,omitempty"`
	MaxItems    int32  `xml:"MaxItems"`
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
	// NextRecordIdentifier continues a weighted/latency/failover/geo record set that
	// straddles a page boundary: it names the exact SetIdentifier the next page
	// resumes from so a multi-value group is never re-emitted or skipped.
	NextRecordIdentifier string `xml:"NextRecordIdentifier,omitempty"`
	MaxItems             int32  `xml:"MaxItems"`
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
			Comment:     info.Comment,
			PrivateZone: info.Private,
		},
		ResourceRecordSetCount: int64(info.RecordCount),
	}
}

// toVPCsXML converts a zone's driver VPC associations into their Route 53 VPC
// elements, or nil when the zone has none (a public zone).
func toVPCsXML(vpcs []dnsdriver.VPCAssociation) []vpcXML {
	if len(vpcs) == 0 {
		return nil
	}

	out := make([]vpcXML, 0, len(vpcs))
	for _, v := range vpcs {
		out = append(out, vpcXML{VPCRegion: v.VPCRegion, VPCId: v.VPCID})
	}

	return out
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
