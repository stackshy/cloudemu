package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Route target types understood by the driver; mirror the AWS route-target
// taxonomy. Phase 2 wires gateway (IGW) + nat-gateway + peering targets.
const (
	targetTypeGateway    = "gateway"
	targetTypeNatGateway = "nat-gateway"
	targetTypePeering    = "peering"
)

type routeXML struct {
	DestinationCIDR      string `xml:"destinationCidrBlock"`
	GatewayID            string `xml:"gatewayId,omitempty"`
	NatGatewayID         string `xml:"natGatewayId,omitempty"`
	VpcPeeringConnection string `xml:"vpcPeeringConnectionId,omitempty"`
	State                string `xml:"state"`
}

type rtAssociationXML struct {
	AssociationID string `xml:"routeTableAssociationId"`
	RouteTableID  string `xml:"routeTableId"`
	SubnetID      string `xml:"subnetId,omitempty"`
	Main          bool   `xml:"main"`
}

type routeTableXML struct {
	RouteTableID string             `xml:"routeTableId"`
	VpcID        string             `xml:"vpcId"`
	Routes       []routeXML         `xml:"routeSet>item,omitempty"`
	Associations []rtAssociationXML `xml:"associationSet>item,omitempty"`
	Tags         []tagItem          `xml:"tagSet>item,omitempty"`
}

type createRouteTableResponseXML struct {
	XMLName    xml.Name      `xml:"CreateRouteTableResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	RouteTable routeTableXML `xml:"routeTable"`
}

type describeRouteTablesResponseXML struct {
	XMLName       xml.Name        `xml:"DescribeRouteTablesResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	RouteTableSet []routeTableXML `xml:"routeTableSet>item"`
}

type createRouteResponseXML struct {
	XMLName   xml.Name `xml:"CreateRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteRouteResponseXML struct {
	XMLName   xml.Name `xml:"DeleteRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteRouteTableResponseXML struct {
	XMLName   xml.Name `xml:"DeleteRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type associateRouteTableResponseXML struct {
	XMLName       xml.Name `xml:"AssociateRouteTableResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	AssociationID string   `xml:"associationId"`
}

type disassociateRouteTableResponseXML struct {
	XMLName   xml.Name `xml:"DisassociateRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createRouteTable(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.RouteTableConfig{
		VPCID: r.Form.Get("VpcId"),
		Tags:  mergeTagSpecs(awsquery.TagSpecs(r.Form), "route-table"),
	}

	rt, err := h.vpc.CreateRouteTable(r.Context(), cfg)
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createRouteTableResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		RouteTable: toRouteTableXML(rt),
	})
}

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/sg/igw
func (h *Handler) describeRouteTables(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "RouteTableId")

	rts, err := h.vpc.DescribeRouteTables(r.Context(), ids)
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	out := make([]routeTableXML, 0, len(rts))
	for i := range rts {
		out = append(out, toRouteTableXML(&rts[i]))
	}

	awsquery.WriteXMLResponse(w, describeRouteTablesResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		RouteTableSet: out,
	})
}

func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request) {
	// Real EC2 accepts many target types; Phase 2 wires only IGW. Other
	// targets return a 400 until the later phases land them.
	target, targetType := resolveRouteTarget(r)
	if target == "" {
		writeRouteTableErr(w, newInvalidParameterErr(
			"one of GatewayId / NatGatewayId / VpcPeeringConnectionId is required"))

		return
	}

	err := h.vpc.CreateRoute(r.Context(),
		r.Form.Get("RouteTableId"),
		r.Form.Get("DestinationCidrBlock"),
		target, targetType)
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createRouteResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	err := h.vpc.DeleteRoute(r.Context(),
		r.Form.Get("RouteTableId"), r.Form.Get("DestinationCidrBlock"))
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRouteResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteRouteTable(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteRouteTable(r.Context(), r.Form.Get("RouteTableId")); err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRouteTableResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) associateRouteTable(w http.ResponseWriter, r *http.Request) {
	assoc, err := h.vpc.AssociateRouteTable(r.Context(),
		r.Form.Get("RouteTableId"), r.Form.Get("SubnetId"))
	if err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, associateRouteTableResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		AssociationID: assoc.ID,
	})
}

func (h *Handler) disassociateRouteTable(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DisassociateRouteTable(r.Context(), r.Form.Get("AssociationId")); err != nil {
		writeRouteTableErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, disassociateRouteTableResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// resolveRouteTarget picks the first non-empty target the caller supplied and
// maps it to the driver's target-type string. Phase 2 supports IGW and NAT;
// peering, transit gateways, and others come in later phases.
func resolveRouteTarget(r *http.Request) (target, targetType string) {
	if id := r.Form.Get("GatewayId"); id != "" {
		return id, targetTypeGateway
	}

	if id := r.Form.Get("NatGatewayId"); id != "" {
		return id, targetTypeNatGateway
	}

	if id := r.Form.Get("VpcPeeringConnectionId"); id != "" {
		return id, targetTypePeering
	}

	return "", ""
}

func toRouteTableXML(rt *netdriver.RouteTable) routeTableXML {
	x := routeTableXML{
		RouteTableID: rt.ID,
		VpcID:        rt.VPCID,
		Tags:         toTagItems(rt.Tags),
	}

	for _, a := range rt.Associations {
		x.Associations = append(x.Associations, rtAssociationXML{
			AssociationID: a.ID,
			RouteTableID:  nonEmpty(a.RouteTableID, rt.ID),
			SubnetID:      a.SubnetID,
			Main:          a.Main,
		})
	}

	for _, route := range rt.Routes {
		rx := routeXML{
			DestinationCIDR: route.DestinationCIDR,
			State:           nonEmpty(route.State, "active"),
		}

		switch route.TargetType {
		case targetTypeGateway:
			rx.GatewayID = route.TargetID
		case targetTypeNatGateway:
			rx.NatGatewayID = route.TargetID
		case targetTypePeering:
			rx.VpcPeeringConnection = route.TargetID
		}

		x.Routes = append(x.Routes, rx)
	}

	return x
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}

func writeRouteTableErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidRouteTableID.NotFound", "DependencyViolation")
}
