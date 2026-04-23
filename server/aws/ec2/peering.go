package ec2

import (
	"encoding/xml"
	"net/http"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type peeringStatusXML struct {
	Code string `xml:"code"`
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
	VpcID string `xml:"vpcId"`
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
		p = toPeeringXML(&peerings[0])
	}

	awsquery.WriteXMLResponse(w, acceptPeeringResponseXML{
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		VpcPeeringConnection: p,
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

//nolint:dupl // per-resource describe pattern
func (h *Handler) describeVpcPeeringConnections(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VpcPeeringConnectionId")

	peerings, err := h.vpc.DescribePeeringConnections(r.Context(), ids)
	if err != nil {
		writePeeringErr(w, err)
		return
	}

	out := make([]peeringConnectionXML, 0, len(peerings))
	for i := range peerings {
		out = append(out, toPeeringXML(&peerings[i]))
	}

	awsquery.WriteXMLResponse(w, describePeeringResponseXML{
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		VpcPeeringConnection: out,
	})
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

func writePeeringErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpcPeeringConnectionID.NotFound", "DependencyViolation")
}
