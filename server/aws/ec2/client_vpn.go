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

//nolint:gocyclo // flat action dispatch table
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
	case "DescribeClientVpnTargetNetworks":
		h.describeClientVPNTargetNetworks(w, r, c)
	case "AuthorizeClientVpnIngress":
		h.authorizeClientVPNIngress(w, r, c)
	case "RevokeClientVpnIngress":
		h.revokeClientVPNIngress(w, r, c)
	case "DescribeClientVpnAuthorizationRules":
		h.describeClientVPNAuthRules(w, r, c)
	case "CreateClientVpnRoute":
		h.createClientVPNRoute(w, r, c)
	case "DeleteClientVpnRoute":
		h.deleteClientVPNRoute(w, r, c)
	case "DescribeClientVpnRoutes":
		h.describeClientVPNRoutes(w, r, c)
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

type clientVPNTargetNetworkXML struct {
	AssociationID       string             `xml:"associationId"`
	ClientVpnEndpointID string             `xml:"clientVpnEndpointId"`
	TargetNetworkID     string             `xml:"targetNetworkId"`
	VpcID               string             `xml:"vpcId,omitempty"`
	Status              clientVPNStatusXML `xml:"status"`
}

type clientVPNAuthRuleXML struct {
	ClientVpnEndpointID string             `xml:"clientVpnEndpointId"`
	DestinationCidr     string             `xml:"destinationCidr"`
	GroupID             string             `xml:"groupId,omitempty"`
	AccessAll           bool               `xml:"accessAll"`
	Status              clientVPNStatusXML `xml:"status"`
}

type clientVPNRouteXML struct {
	ClientVpnEndpointID string             `xml:"clientVpnEndpointId"`
	DestinationCidr     string             `xml:"destinationCidr"`
	TargetSubnet        string             `xml:"targetSubnet"`
	Status              clientVPNStatusXML `xml:"status"`
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeClientVPNTargetNetworks(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	items, err := c.DescribeClientVPNTargetNetworks(r.Context(), r.Form.Get("ClientVpnEndpointId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	out := make([]clientVPNTargetNetworkXML, 0, len(items))
	for i := range items {
		out = append(out, clientVPNTargetNetworkXML{
			AssociationID: items[i].AssociationID, ClientVpnEndpointID: items[i].EndpointID,
			TargetNetworkID: items[i].SubnetID, VpcID: items[i].VPCID,
			Status: clientVPNStatusXML{Code: items[i].State},
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                    `xml:"DescribeClientVpnTargetNetworksResponse"`
		Xmlns   string                      `xml:"xmlns,attr"`
		Req     string                      `xml:"requestId"`
		Set     []clientVPNTargetNetworkXML `xml:"clientVpnTargetNetworks>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) authorizeClientVPNIngress(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	out, err := c.AuthorizeClientVPNIngress(r.Context(), r.Form.Get("ClientVpnEndpointId"),
		r.Form.Get("TargetNetworkCidr"), r.Form.Get("AccessGroupId"), r.Form.Get("AuthorizeAllGroups") == formTrue)
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"AuthorizeClientVpnIngressResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Status  clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Status: clientVPNStatusXML{Code: out.Status}})
}

func (*Handler) revokeClientVPNIngress(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	err := c.RevokeClientVPNIngress(r.Context(), r.Form.Get("ClientVpnEndpointId"), r.Form.Get("TargetNetworkCidr"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"RevokeClientVpnIngressResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Status  clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Status: clientVPNStatusXML{Code: "revoking"}})
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeClientVPNAuthRules(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	items, err := c.DescribeClientVPNAuthorizationRules(r.Context(), r.Form.Get("ClientVpnEndpointId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	out := make([]clientVPNAuthRuleXML, 0, len(items))
	for i := range items {
		out = append(out, clientVPNAuthRuleXML{
			ClientVpnEndpointID: items[i].EndpointID, DestinationCidr: items[i].TargetCIDR,
			GroupID: items[i].GroupID, AccessAll: items[i].AccessAll,
			Status: clientVPNStatusXML{Code: items[i].Status},
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"DescribeClientVpnAuthorizationRulesResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Set     []clientVPNAuthRuleXML `xml:"authorizationRule>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) createClientVPNRoute(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	out, err := c.CreateClientVPNRoute(r.Context(), r.Form.Get("ClientVpnEndpointId"),
		r.Form.Get("DestinationCidrBlock"), r.Form.Get("TargetVpcSubnetId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"CreateClientVpnRouteResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Status  clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Status: clientVPNStatusXML{Code: out.Status}})
}

func (*Handler) deleteClientVPNRoute(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	err := c.DeleteClientVPNRoute(r.Context(), r.Form.Get("ClientVpnEndpointId"),
		r.Form.Get("DestinationCidrBlock"), r.Form.Get("TargetVpcSubnetId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"DeleteClientVpnRouteResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Status  clientVPNStatusXML `xml:"status"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Status: clientVPNStatusXML{Code: "deleting"}})
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeClientVPNRoutes(w http.ResponseWriter, r *http.Request, c netdriver.ClientVPN) {
	items, err := c.DescribeClientVPNRoutes(r.Context(), r.Form.Get("ClientVpnEndpointId"))
	if err != nil {
		writeClientVPNErr(w, err)
		return
	}

	out := make([]clientVPNRouteXML, 0, len(items))
	for i := range items {
		out = append(out, clientVPNRouteXML{
			ClientVpnEndpointID: items[i].EndpointID, DestinationCidr: items[i].DestinationCIDR,
			TargetSubnet: items[i].TargetSubnetID, Status: clientVPNStatusXML{Code: items[i].Status},
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name            `xml:"DescribeClientVpnRoutesResponse"`
		Xmlns   string              `xml:"xmlns,attr"`
		Req     string              `xml:"requestId"`
		Set     []clientVPNRouteXML `xml:"routes>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
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
