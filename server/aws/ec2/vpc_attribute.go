package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type modifyVpcAttributeResponseXML struct {
	XMLName   xml.Name `xml:"ModifyVpcAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type vpcAttributeBoolXML struct {
	Value bool `xml:"value"`
}

type describeVpcAttributeResponseXML struct {
	XMLName                          xml.Name             `xml:"DescribeVpcAttributeResponse"`
	Xmlns                            string               `xml:"xmlns,attr"`
	RequestID                        string               `xml:"requestId"`
	VpcID                            string               `xml:"vpcId"`
	EnableDNSSupport                 *vpcAttributeBoolXML `xml:"enableDnsSupport,omitempty"`
	EnableDNSHostnames               *vpcAttributeBoolXML `xml:"enableDnsHostnames,omitempty"`
	EnableNetworkAddressUsageMetrics *vpcAttributeBoolXML `xml:"enableNetworkAddressUsageMetrics,omitempty"`
}

// describeVpcAttribute returns one DNS attribute of a VPC. Like the real API it
// answers exactly the requested Attribute (enableDnsSupport or
// enableDnsHostnames); the aws_vpc resource reads both back after create, so an
// absent handler makes VPC creation fail outright.
func (h *Handler) describeVpcAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("VpcId")

	vpcs, err := h.vpc.DescribeVPCs(r.Context(), []string{id})
	if err != nil {
		writeVPCErr(w, err)
		return
	}

	if len(vpcs) == 0 {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidVpcID.NotFound",
			"The vpc ID '"+id+"' does not exist")

		return
	}

	resp := describeVpcAttributeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VpcID:     id,
	}

	attr := r.Form.Get("Attribute")
	if attr == "" {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "MissingParameter",
			"The request must contain the parameter Attribute")

		return
	}

	switch attr {
	case "enableDnsSupport":
		resp.EnableDNSSupport = &vpcAttributeBoolXML{Value: vpcs[0].EnableDNSSupport}
	case "enableDnsHostnames":
		resp.EnableDNSHostnames = &vpcAttributeBoolXML{Value: vpcs[0].EnableDNSHostnames}
	case "enableNetworkAddressUsageMetrics":
		// Not modeled; the resource default is off.
		resp.EnableNetworkAddressUsageMetrics = &vpcAttributeBoolXML{Value: false}
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue",
			"Invalid value for VPC attribute")

		return
	}

	awsquery.WriteXMLResponse(w, resp)
}

// modifyVpcAttribute sets one DNS attribute of a VPC.
//
// The real API accepts exactly one attribute per call and leaves the other
// untouched, so an absent parameter must mean "unchanged" rather than "false"
// — a caller enabling DNS hostnames would otherwise silently turn DNS support
// off.
func (h *Handler) modifyVpcAttribute(w http.ResponseWriter, r *http.Request) {
	support := boolAttributeValue(r, "EnableDnsSupport")
	hostnames := boolAttributeValue(r, "EnableDnsHostnames")

	attrs, ok := h.vpc.(netdriver.VPCAttributes)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"this driver does not model VPC attributes")

		return
	}

	err := attrs.ModifyVPCAttribute(r.Context(), r.Form.Get("VpcId"),
		netdriver.VPCAttributeUpdate{
			EnableDNSSupport:   support,
			EnableDNSHostnames: hostnames,
		})
	if err != nil {
		writeVPCErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyVpcAttributeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// boolAttributeValue reads an AttributeBooleanValue parameter, returning nil
// when the caller did not supply it.
func boolAttributeValue(r *http.Request, name string) *bool {
	raw := r.Form.Get(name + ".Value")
	if raw == "" {
		return nil
	}

	v := raw == "true"

	return &v
}
