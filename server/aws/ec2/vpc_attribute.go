package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type modifyVpcAttributeResponseXML struct {
	XMLName   xml.Name `xml:"ModifyVpcAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
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

	err := h.vpc.ModifyVPCAttribute(r.Context(), r.Form.Get("VpcId"), support, hostnames)
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
