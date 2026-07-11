package route53

import (
	"encoding/xml"
	"strings"

	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
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

// changeID is the synthetic ChangeInfo id returned for mutating operations;
// the SDK's change-tracking poller treats an INSYNC change as done.
const changeID = "/change/C0000000000000000000"

// --- shared record element ---

// resourceRecordXML is a single record value (<ResourceRecord><Value>…).
type resourceRecordXML struct {
	Value string `xml:"Value"`
}

// resourceRecordSetXML is the Route 53 ResourceRecordSet element.
type resourceRecordSetXML struct {
	Name            string              `xml:"Name"`
	Type            string              `xml:"Type"`
	SetIdentifier   string              `xml:"SetIdentifier,omitempty"`
	Weight          *int64              `xml:"Weight,omitempty"`
	TTL             *int64              `xml:"TTL,omitempty"`
	ResourceRecords []resourceRecordXML `xml:"ResourceRecords>ResourceRecord,omitempty"`
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
	XMLName    xml.Name      `xml:"CreateHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	HostedZone hostedZoneXML `xml:"HostedZone"`
	ChangeInfo changeInfoXML `xml:"ChangeInfo"`
}

type getHostedZoneResponse struct {
	XMLName    xml.Name      `xml:"GetHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	HostedZone hostedZoneXML `xml:"HostedZone"`
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
// The dns driver does not persist the caller-supplied CallerReference, so on
// Get/List we surface the zone name (a stable, meaningful value) rather than
// leaking the internal zone id. CreateHostedZone overrides this with the actual
// reference the caller sent so a create round-trip is faithful.
func toHostedZoneXML(info *dnsdriver.ZoneInfo) hostedZoneXML {
	return hostedZoneXML{
		Id:              info.ID,
		Name:            info.Name,
		CallerReference: info.Name,
		Config: &hostedZoneConfigXML{
			PrivateZone: info.Private,
		},
		ResourceRecordSetCount: int64(info.RecordCount),
	}
}

// toRecordSetXML converts a driver record into its Route 53 element.
func toRecordSetXML(rec *dnsdriver.RecordInfo) resourceRecordSetXML {
	ttl := int64(rec.TTL)

	rrs := make([]resourceRecordXML, 0, len(rec.Values))
	for _, v := range rec.Values {
		rrs = append(rrs, resourceRecordXML{Value: v})
	}

	out := resourceRecordSetXML{
		Name:            rec.Name,
		Type:            rec.Type,
		SetIdentifier:   rec.SetID,
		TTL:             &ttl,
		ResourceRecords: rrs,
	}

	if rec.Weight != nil {
		w := int64(*rec.Weight)
		out.Weight = &w
	}

	return out
}
