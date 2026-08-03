package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) clientVPN() (netdriver.ClientVPN, bool) {
	c, ok := h.vpc.(netdriver.ClientVPN)

	return c, ok
}

type clientVPNStatusXML struct {
	Code string `xml:"code"`
}

type clientVPNEndpointXML struct {
	ClientVpnEndpointID  string             `xml:"clientVpnEndpointId"`
	Description          string             `xml:"description,omitempty"`
	Status               clientVPNStatusXML `xml:"status"`
	ClientCidrBlock      string             `xml:"clientCidrBlock"`
	ServerCertificateARN string             `xml:"serverCertificateArn"`
	SplitTunnel          bool               `xml:"splitTunnel"`
	VpcID                string             `xml:"vpcId,omitempty"`
	Tags                 []tagItem          `xml:"tagSet>item,omitempty"`
}

func (h *Handler) routeClientVPN(w http.ResponseWriter, r *http.Request, action string) bool {
	c, ok := h.clientVPN()
	if !ok {
		return false
	}

	switch action {
	case "CreateClientVpnEndpoint":
		h.createClientVPNEndpoint(w, r, c)
	case "DeleteClientVpnEndpoint":
		h.deleteClientVPNEndpoint(w, r, c)
	case "DescribeClientVpnEndpoints":
		h.describeClientVPNEndpoints(w, r, c)
	case "AssociateClientVpnTargetNetwork":
		h.associateClientVPN(w, r, c)
	case "DisassociateClientVpnTargetNetwork":
		h.disassociateClientVPN(w, r, c)
	default:
		return false
	}

	return true
}

func (*Handler) createClientVPNEndpoint(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	out, err := c.CreateClientVPNEndpoint(r.Context(), netdriver.ClientVPNEndpointConfig{
		Description:          r.Form.Get("Description"),
		ClientCIDRBlock:      r.Form.Get("ClientCidrBlock"),
		ServerCertificateARN: r.Form.Get("ServerCertificateArn"),
		SplitTunnel:          r.Form.Get("SplitTunnel") == formTrue,
		Tags:                 mergeTagSpecs(awsquery.TagSpecs(r.Form), "client-vpn-endpoint"),
	})
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name           `xml:"CreateClientVpnEndpointResponse"`
		Xmlns      string             `xml:"xmlns,attr"`
		Req        string             `xml:"requestId"`
		EndpointID string             `xml:"clientVpnEndpointId"`
		Status     clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, EndpointID: out.ID, Status: clientVPNStatusXML{Code: out.State}})
}

func (*Handler) deleteClientVPNEndpoint(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	if err := c.DeleteClientVPNEndpoint(r.Context(), r.Form.Get("ClientVpnEndpointId")); err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"DeleteClientVpnEndpointResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Status  clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Status: clientVPNStatusXML{Code: "deleting"}})
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeClientVPNEndpoints(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	items, err := c.DescribeClientVPNEndpoints(r.Context(), awsquery.ListStrings(r.Form, "ClientVpnEndpointId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	out := make([]clientVPNEndpointXML, 0, len(items))
	for i := range items {
		out = append(out, toClientVPNEndpointXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"DescribeClientVpnEndpointsResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Set     []clientVPNEndpointXML `xml:"clientVpnEndpoint>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) associateClientVPN(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	out, err := c.AssociateClientVPNTargetNetwork(r.Context(), r.Form.Get("ClientVpnEndpointId"), r.Form.Get("SubnetId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName       xml.Name           `xml:"AssociateClientVpnTargetNetworkResponse"`
		Xmlns         string             `xml:"xmlns,attr"`
		Req           string             `xml:"requestId"`
		AssociationID string             `xml:"associationId"`
		Status        clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, AssociationID: out.AssociationID, Status: clientVPNStatusXML{Code: out.State}})
}

func (*Handler) disassociateClientVPN(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	if err := c.DisassociateClientVPNTargetNetwork(r.Context(), r.Form.Get("ClientVpnEndpointId"), r.Form.Get("AssociationId")); err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"DisassociateClientVpnTargetNetworkResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Status  clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Status: clientVPNStatusXML{Code: "disassociating"}})
}

func toClientVPNEndpointXML(e *netdriver.ClientVPNEndpoint) clientVPNEndpointXML {
	return clientVPNEndpointXML{
		ClientVpnEndpointID: e.ID, Description: e.Description, Status: clientVPNStatusXML{Code: e.State},
		ClientCidrBlock: e.ClientCIDRBlock, ServerCertificateARN: e.ServerCertificateARN,
		SplitTunnel: e.SplitTunnel, VpcID: e.VPCID, Tags: toTagItems(e.Tags),
	}
}

func writeClientVPNErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidClientVpnEndpointId.NotFound", "IncorrectState")
}
