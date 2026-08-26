package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type peeringStatusXML struct {
	Code    string `xml:"code"`
	Message string `xml:"message,omitempty"`
}

type peeringConnectionXML struct {
	VpcPeeringConnectionID string           `xml:"vpcPeeringConnectionId"`
	RequesterVpcInfo       peeringVpcInfo   `xml:"requesterVpcInfo"`
	AccepterVpcInfo        peeringVpcInfo   `xml:"accepterVpcInfo"`
	Status                 peeringStatusXML `xml:"status"`
	CreationTime           string           `xml:"creationTimestamp,omitempty"`
	Tags                   []tagItem        `xml:"tagSet>item,omitempty"`
}

type peeringVpcInfo struct {
	VpcID     string `xml:"vpcId"`
	OwnerID   string `xml:"ownerId,omitempty"`
	CidrBlock string `xml:"cidrBlock,omitempty"`
	Region    string `xml:"region,omitempty"`
}

type createPeeringResponseXML struct {
	XMLName              xml.Name             `xml:"CreateVpcPeeringConnectionResponse"`
	Xmlns                string               `xml:"xmlns,attr"`
	RequestID            string               `xml:"requestId"`
	VpcPeeringConnection peeringConnectionXML `xml:"vpcPeeringConnection"`
}

type acceptPeeringResponseXML struct {
	XMLName              xml.Name             `xml:"AcceptVpcPeeringConnectionResponse"`
	Xmlns                string               `xml:"xmlns,attr"`
	RequestID            string               `xml:"requestId"`
	VpcPeeringConnection peeringConnectionXML `xml:"vpcPeeringConnection"`
}

type deletePeeringResponseXML struct {
	XMLName   xml.Name `xml:"DeleteVpcPeeringConnectionResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type rejectPeeringResponseXML struct {
	XMLName   xml.Name `xml:"RejectVpcPeeringConnectionResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type describePeeringResponseXML struct {
	XMLName              xml.Name               `xml:"DescribeVpcPeeringConnectionsResponse"`
	Xmlns                string                 `xml:"xmlns,attr"`
	RequestID            string                 `xml:"requestId"`
	VpcPeeringConnection []peeringConnectionXML `xml:"vpcPeeringConnectionSet>item"`
}

//nolint:dupl // per-resource create pattern; mirrors snapshot/flow-log shape
func (h *Handler) createVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.PeeringConfig{
		RequesterVPC: r.Form.Get("VpcId"),
		AccepterVPC:  r.Form.Get("PeerVpcId"),
		Tags:         mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc-peering-connection"),
	}

	info, err := h.vpc.CreatePeeringConnection(r.Context(), cfg)
	if err != nil {
		writePeeringErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createPeeringResponseXML{
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		VpcPeeringConnection: toPeeringXML(info),
	})
}

func (h *Handler) acceptVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("VpcPeeringConnectionId")

	if err := h.vpc.AcceptPeeringConnection(r.Context(), id); err != nil {
		writePeeringErr(w, err)
		return
	}

	peerings, _ := h.vpc.DescribePeeringConnections(r.Context(), []string{id})

	var p peeringConnectionXML

	if len(peerings) > 0 {
		p = h.enrichedPeeringXML(r, &peerings[0])
	}

	awsquery.WriteXMLResponse(w, acceptPeeringResponseXML{
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		VpcPeeringConnection: p,
	})
}

func (h *Handler) rejectVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.RejectPeeringConnection(r.Context(),
		r.Form.Get("VpcPeeringConnectionId")); err != nil {
		writePeeringErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, rejectPeeringResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeletePeeringConnection(r.Context(),
		r.Form.Get("VpcPeeringConnectionId")); err != nil {
		writePeeringErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deletePeeringResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) describeVpcPeeringConnections(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VpcPeeringConnectionId")

	peerings, err := h.vpc.DescribePeeringConnections(r.Context(), ids)
	if err != nil {
		writePeeringErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateNetworkingFilters(filters, peeringFilterMatch); err != nil {
		writePeeringErr(w, err)
		return
	}

	out := make([]peeringConnectionXML, 0, len(peerings))

	for i := range peerings {
		if !matchNetworkingFilters(&peerings[i], filters, peeringFilterMatch) {
			continue
		}

		out = append(out, h.enrichedPeeringXML(r, &peerings[i]))
	}

	awsquery.WriteXMLResponse(w, describePeeringResponseXML{
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		VpcPeeringConnection: out,
	})
}

// peeringFilterMatch reports whether p satisfies filter f and whether f is a
// filter DescribeVpcPeeringConnections recognizes. Terraform's
// aws_vpc_peering_connection data source looks a connection up by status-code and
// requester/accepter vpc id; without honoring the filter every connection returns.
func peeringFilterMatch(p *netdriver.PeeringConnection, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case "vpc-peering-connection-id":
		return containsString(f.Values, p.ID), true
	case "status-code":
		return containsString(f.Values, p.Status), true
	case "requester-vpc-info.vpc-id":
		return containsString(f.Values, p.RequesterVPC), true
	case "accepter-vpc-info.vpc-id":
		return containsString(f.Values, p.AccepterVPC), true
	default:
		return tagFilterMatch(f.Name, f.Values, p.Tags)
	}
}

func toPeeringXML(p *netdriver.PeeringConnection) peeringConnectionXML {
	return peeringConnectionXML{
		VpcPeeringConnectionID: p.ID,
		RequesterVpcInfo:       peeringVpcInfo{VpcID: p.RequesterVPC},
		AccepterVpcInfo:        peeringVpcInfo{VpcID: p.AccepterVPC},
		Status:                 peeringStatusXML{Code: p.Status},
		CreationTime:           p.CreatedAt,
		Tags:                   toTagItems(p.Tags),
	}
}

// enrichedPeeringXML is the Describe/Accept projection: it fills the
// requester/accepter VpcInfo (ownerId, cidrBlock, region) and the status
// message that IaC reads back, resolving each VPC's CIDR from the driver.
func (h *Handler) enrichedPeeringXML(r *http.Request, p *netdriver.PeeringConnection) peeringConnectionXML {
	region := regionFromRequest(r)

	return peeringConnectionXML{
		VpcPeeringConnectionID: p.ID,
		RequesterVpcInfo:       h.peeringVpcInfo(r, p.RequesterVPC, region),
		AccepterVpcInfo:        h.peeringVpcInfo(r, p.AccepterVPC, region),
		Status:                 peeringStatusXML{Code: p.Status, Message: peeringStatusMessage(p.Status)},
		CreationTime:           p.CreatedAt,
		Tags:                   toTagItems(p.Tags),
	}
}

// peeringVpcInfo fills the requester/accepter block AWS returns, resolving the
// VPC's CIDR from the networking driver so IaC that reads accepter/requester
// CIDRs (Terraform aws_vpc_peering_connection) sees real values.
func (h *Handler) peeringVpcInfo(r *http.Request, vpcID, region string) peeringVpcInfo {
	info := peeringVpcInfo{VpcID: vpcID, OwnerID: ownerID, Region: region}
	if vpcID == "" {
		return info
	}

	vpcs, err := h.vpc.DescribeVPCs(r.Context(), []string{vpcID})
	if err == nil && len(vpcs) > 0 {
		info.CidrBlock = vpcs[0].CIDRBlock
	}

	return info
}

// peeringStatusMessage mirrors the human-readable status message AWS attaches to
// each peering status code.
func peeringStatusMessage(code string) string {
	switch code {
	case "pending-acceptance":
		return "Pending Acceptance by " + ownerID
	case "active":
		return "Active"
	case "rejected":
		return "Inactive"
	case "deleted":
		return "Deleted"
	default:
		return ""
	}
}

// writePeeringErr maps peering errors. A state precondition (accepting/rejecting
// a connection that is not pending-acceptance) is InvalidStateTransition — real
// EC2's code for "not in the correct state" — not a dependency violation.
func writePeeringErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpcPeeringConnectionID.NotFound", "InvalidStateTransition")
}
