package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) vpnConnections() (netdriver.VPNConnections, bool) {
	v, ok := h.vpc.(netdriver.VPNConnections)

	return v, ok
}

type customerGatewayXML struct {
	CustomerGatewayID string    `xml:"customerGatewayId"`
	IPAddress         string    `xml:"ipAddress"`
	BgpAsn            string    `xml:"bgpAsn"`
	Type              string    `xml:"type"`
	State             string    `xml:"state"`
	Tags              []tagItem `xml:"tagSet>item,omitempty"`
}

type vpnGatewayXML struct {
	VpnGatewayID  string             `xml:"vpnGatewayId"`
	Type          string             `xml:"type"`
	State         string             `xml:"state"`
	AmazonSideAsn int64              `xml:"amazonSideAsn,omitempty"`
	Attachments   []vpnAttachmentXML `xml:"attachments>item,omitempty"`
	Tags          []tagItem          `xml:"tagSet>item,omitempty"`
}

type vpnAttachmentXML struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type vpnConnectionXML struct {
	VpnConnectionID   string    `xml:"vpnConnectionId"`
	CustomerGatewayID string    `xml:"customerGatewayId"`
	VpnGatewayID      string    `xml:"vpnGatewayId,omitempty"`
	TransitGatewayID  string    `xml:"transitGatewayId,omitempty"`
	Type              string    `xml:"type"`
	State             string    `xml:"state"`
	Tags              []tagItem `xml:"tagSet>item,omitempty"`
}

//nolint:gocyclo // flat action dispatch table
func (h *Handler) routeVPN(w http.ResponseWriter, r *http.Request, action string) bool {
	v, ok := h.vpnConnections()
	if !ok {
		return false
	}

	switch action {
	case "CreateCustomerGateway":
		h.createCustomerGateway(w, r, v)
	case "DeleteCustomerGateway":
		h.deleteCustomerGateway(w, r, v)
	case "DescribeCustomerGateways":
		h.describeCustomerGateways(w, r, v)
	case "CreateVpnGateway":
		h.createVPNGateway(w, r, v)
	case "DeleteVpnGateway":
		h.deleteVPNGateway(w, r, v)
	case "DescribeVpnGateways":
		h.describeVPNGateways(w, r, v)
	case "AttachVpnGateway":
		h.attachVPNGateway(w, r, v)
	case "DetachVpnGateway":
		h.detachVPNGateway(w, r, v)
	case "CreateVpnConnection":
		h.createVPNConnection(w, r, v)
	case "DeleteVpnConnection":
		h.deleteVPNConnection(w, r, v)
	case "DescribeVpnConnections":
		h.describeVPNConnections(w, r, v)
	default:
		return false
	}

	return true
}

func (h *Handler) createCustomerGateway(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	asn, _ := strconv.ParseInt(r.Form.Get("BgpAsn"), 10, 64)
	out, err := v.CreateCustomerGateway(r.Context(), netdriver.CustomerGatewayConfig{
		IPAddress: nonEmpty(r.Form.Get("PublicIp"), r.Form.Get("IpAddress")),
		BGPASN:    asn,
		Type:      r.Form.Get("Type"),
		Tags:      mergeTagSpecs(awsquery.TagSpecs(r.Form), "customer-gateway"),
	})
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"CreateCustomerGatewayResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		CGW     customerGatewayXML `xml:"customerGateway"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, CGW: toCustomerGatewayXML(out)})
}

func (h *Handler) deleteCustomerGateway(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	if err := v.DeleteCustomerGateway(r.Context(), r.Form.Get("CustomerGatewayId")); err != nil {
		writeVPNErr(w, err)
		return
	}

	writeReturnTrue(w, "DeleteCustomerGatewayResponse")
}

func (h *Handler) describeCustomerGateways(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	items, err := v.DescribeCustomerGateways(r.Context(), awsquery.ListStrings(r.Form, "CustomerGatewayId"))
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	out := make([]customerGatewayXML, 0, len(items))
	for i := range items {
		out = append(out, toCustomerGatewayXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name             `xml:"DescribeCustomerGatewaysResponse"`
		Xmlns   string               `xml:"xmlns,attr"`
		Req     string               `xml:"requestId"`
		Set     []customerGatewayXML `xml:"customerGatewaySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (h *Handler) createVPNGateway(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	asn, _ := strconv.ParseInt(r.Form.Get("AmazonSideAsn"), 10, 64)
	out, err := v.CreateVPNGateway(r.Context(), netdriver.VPNGatewayConfig{
		Type:          r.Form.Get("Type"),
		AmazonSideASN: asn,
		Tags:          mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpn-gateway"),
	})
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name      `xml:"CreateVpnGatewayResponse"`
		Xmlns   string        `xml:"xmlns,attr"`
		Req     string        `xml:"requestId"`
		VGW     vpnGatewayXML `xml:"vpnGateway"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, VGW: toVPNGatewayXML(out)})
}

func (h *Handler) deleteVPNGateway(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	if err := v.DeleteVPNGateway(r.Context(), r.Form.Get("VpnGatewayId")); err != nil {
		writeVPNErr(w, err)
		return
	}

	writeReturnTrue(w, "DeleteVpnGatewayResponse")
}

func (h *Handler) describeVPNGateways(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	items, err := v.DescribeVPNGateways(r.Context(), awsquery.ListStrings(r.Form, "VpnGatewayId"))
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	out := make([]vpnGatewayXML, 0, len(items))
	for i := range items {
		out = append(out, toVPNGatewayXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name        `xml:"DescribeVpnGatewaysResponse"`
		Xmlns   string          `xml:"xmlns,attr"`
		Req     string          `xml:"requestId"`
		Set     []vpnGatewayXML `xml:"vpnGatewaySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (h *Handler) attachVPNGateway(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	out, err := v.AttachVPNGateway(r.Context(), r.Form.Get("VpnGatewayId"), r.Form.Get("VpcId"))
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name         `xml:"AttachVpnGatewayResponse"`
		Xmlns      string           `xml:"xmlns,attr"`
		Req        string           `xml:"requestId"`
		Attachment vpnAttachmentXML `xml:"attachment"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Attachment: vpnAttachmentXML{VpcID: out.AttachedVPCID, State: out.AttachmentState}})
}

func (h *Handler) detachVPNGateway(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	if err := v.DetachVPNGateway(r.Context(), r.Form.Get("VpnGatewayId"), r.Form.Get("VpcId")); err != nil {
		writeVPNErr(w, err)
		return
	}

	writeReturnTrue(w, "DetachVpnGatewayResponse")
}

func (h *Handler) createVPNConnection(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	out, err := v.CreateVPNConnection(r.Context(), netdriver.VPNConnectionConfig{
		CustomerGatewayID: r.Form.Get("CustomerGatewayId"),
		VPNGatewayID:      r.Form.Get("VpnGatewayId"),
		TransitGatewayID:  r.Form.Get("TransitGatewayId"),
		Type:              r.Form.Get("Type"),
		StaticRoutesOnly:  r.Form.Get("Options.StaticRoutesOnly") == formTrue,
		Tags:              mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpn-connection"),
	})
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"CreateVpnConnectionResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		VPN     vpnConnectionXML `xml:"vpnConnection"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, VPN: toVPNConnectionXML(out)})
}

func (h *Handler) deleteVPNConnection(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	if err := v.DeleteVPNConnection(r.Context(), r.Form.Get("VpnConnectionId")); err != nil {
		writeVPNErr(w, err)
		return
	}

	writeReturnTrue(w, "DeleteVpnConnectionResponse")
}

func (h *Handler) describeVPNConnections(w http.ResponseWriter, r *http.Request, v netdriver.VPNConnections) {
	items, err := v.DescribeVPNConnections(r.Context(), awsquery.ListStrings(r.Form, "VpnConnectionId"))
	if err != nil {
		writeVPNErr(w, err)
		return
	}

	out := make([]vpnConnectionXML, 0, len(items))
	for i := range items {
		out = append(out, toVPNConnectionXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"DescribeVpnConnectionsResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Set     []vpnConnectionXML `xml:"vpnConnectionSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func toCustomerGatewayXML(c *netdriver.CustomerGateway) customerGatewayXML {
	return customerGatewayXML{
		CustomerGatewayID: c.ID, IPAddress: c.IPAddress, BgpAsn: strconv.FormatInt(c.BGPASN, 10),
		Type: c.Type, State: c.State, Tags: toTagItems(c.Tags),
	}
}

func toVPNGatewayXML(v *netdriver.VPNGateway) vpnGatewayXML {
	x := vpnGatewayXML{
		VpnGatewayID: v.ID, Type: v.Type, State: v.State, AmazonSideAsn: v.AmazonSideASN,
		Tags: toTagItems(v.Tags),
	}
	if v.AttachedVPCID != "" {
		x.Attachments = []vpnAttachmentXML{{VpcID: v.AttachedVPCID, State: v.AttachmentState}}
	}

	return x
}

func toVPNConnectionXML(v *netdriver.VPNConnection) vpnConnectionXML {
	return vpnConnectionXML{
		VpnConnectionID: v.ID, CustomerGatewayID: v.CustomerGatewayID, VpnGatewayID: v.VPNGatewayID,
		TransitGatewayID: v.TransitGatewayID, Type: v.Type, State: v.State, Tags: toTagItems(v.Tags),
	}
}

func writeVPNErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpnGatewayID.NotFound", "IncorrectState")
}
