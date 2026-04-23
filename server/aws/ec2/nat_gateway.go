package ec2

import (
	"encoding/xml"
	"net/http"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type natGatewayXML struct {
	NatGatewayID string    `xml:"natGatewayId"`
	VpcID        string    `xml:"vpcId"`
	SubnetID     string    `xml:"subnetId"`
	State        string    `xml:"state"`
	CreateTime   string    `xml:"createTime,omitempty"`
	Tags         []tagItem `xml:"tagSet>item,omitempty"`
}

type createNatGatewayResponseXML struct {
	XMLName    xml.Name      `xml:"CreateNatGatewayResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	NatGateway natGatewayXML `xml:"natGateway"`
}

type describeNatGatewaysResponseXML struct {
	XMLName       xml.Name        `xml:"DescribeNatGatewaysResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	NatGatewaySet []natGatewayXML `xml:"natGatewaySet>item"`
}

type deleteNatGatewayResponseXML struct {
	XMLName      xml.Name `xml:"DeleteNatGatewayResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	NatGatewayID string   `xml:"natGatewayId"`
}

func (h *Handler) createNatGateway(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.NATGatewayConfig{
		SubnetID: r.Form.Get("SubnetId"),
		Tags:     mergeTagSpecs(awsquery.TagSpecs(r.Form), "natgateway"),
	}

	info, err := h.vpc.CreateNATGateway(r.Context(), cfg)
	if err != nil {
		writeNatErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createNatGatewayResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		NatGateway: toNatGatewayXML(info),
	})
}

func (h *Handler) deleteNatGateway(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("NatGatewayId")

	if err := h.vpc.DeleteNATGateway(r.Context(), id); err != nil {
		writeNatErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteNatGatewayResponseXML{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		NatGatewayID: id,
	})
}

//nolint:dupl // per-resource describe pattern
func (h *Handler) describeNatGateways(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "NatGatewayId")

	nats, err := h.vpc.DescribeNATGateways(r.Context(), ids)
	if err != nil {
		writeNatErr(w, err)
		return
	}

	out := make([]natGatewayXML, 0, len(nats))
	for i := range nats {
		out = append(out, toNatGatewayXML(&nats[i]))
	}

	awsquery.WriteXMLResponse(w, describeNatGatewaysResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		NatGatewaySet: out,
	})
}

func toNatGatewayXML(n *netdriver.NATGateway) natGatewayXML {
	state := n.State
	if state == "" {
		state = stateAvailable
	}

	return natGatewayXML{
		NatGatewayID: n.ID,
		VpcID:        n.VPCID,
		SubnetID:     n.SubnetID,
		State:        state,
		CreateTime:   n.CreatedAt,
		Tags:         toTagItems(n.Tags),
	}
}

func writeNatErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "NatGatewayNotFound", "DependencyViolation")
}
