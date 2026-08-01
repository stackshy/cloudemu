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
