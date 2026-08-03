package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) egressOnlyIGWs() (netdriver.EgressOnlyInternetGateways, bool) {
	e, ok := h.vpc.(netdriver.EgressOnlyInternetGateways)

	return e, ok
}

type egressOnlyIGWAttachmentXML struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type egressOnlyIGWXML struct {
	EgressOnlyInternetGatewayID string                       `xml:"egressOnlyInternetGatewayId"`
	Attachments                 []egressOnlyIGWAttachmentXML `xml:"attachmentSet>item,omitempty"`
	Tags                        []tagItem                    `xml:"tagSet>item,omitempty"`
}

func (h *Handler) routeEgressOnlyIGW(w http.ResponseWriter, r *http.Request, action string) bool {
	e, ok := h.egressOnlyIGWs()
	if !ok {
		return false
	}

	switch action {
	case "CreateEgressOnlyInternetGateway":
		h.createEgressOnlyIGW(w, r, e)
	case "DeleteEgressOnlyInternetGateway":
		h.deleteEgressOnlyIGW(w, r, e)
	case "DescribeEgressOnlyInternetGateways":
		h.describeEgressOnlyIGWs(w, r, e)
	default:
		return false
	}

	return true
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) createEgressOnlyIGW(w http.ResponseWriter, r *http.Request, e netdriver.EgressOnlyInternetGateways) {
	out, err := e.CreateEgressOnlyInternetGateway(r.Context(), r.Form.Get("VpcId"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "egress-only-internet-gateway"))
	if err != nil {
		writeEgressOnlyErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"CreateEgressOnlyInternetGatewayResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		Gateway egressOnlyIGWXML `xml:"egressOnlyInternetGateway"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Gateway: toEgressOnlyIGWXML(out)})
}

func (*Handler) deleteEgressOnlyIGW(w http.ResponseWriter, r *http.Request, e netdriver.EgressOnlyInternetGateways) {
	if err := e.DeleteEgressOnlyInternetGateway(r.Context(), r.Form.Get("EgressOnlyInternetGatewayId")); err != nil {
		writeEgressOnlyErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name `xml:"DeleteEgressOnlyInternetGatewayResponse"`
		Xmlns      string   `xml:"xmlns,attr"`
		Req        string   `xml:"requestId"`
		ReturnCode bool     `xml:"returnCode"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ReturnCode: true})
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeEgressOnlyIGWs(w http.ResponseWriter, r *http.Request, e netdriver.EgressOnlyInternetGateways) {
	items, err := e.DescribeEgressOnlyInternetGateways(r.Context(), awsquery.ListStrings(r.Form, "EgressOnlyInternetGatewayId"))
	if err != nil {
		writeEgressOnlyErr(w, err)
		return
	}

	out := make([]egressOnlyIGWXML, 0, len(items))
	for i := range items {
		out = append(out, toEgressOnlyIGWXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"DescribeEgressOnlyInternetGatewaysResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Set     []egressOnlyIGWXML `xml:"egressOnlyInternetGatewaySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func toEgressOnlyIGWXML(e *netdriver.EgressOnlyInternetGateway) egressOnlyIGWXML {
	return egressOnlyIGWXML{
		EgressOnlyInternetGatewayID: e.ID,
		Attachments:                 []egressOnlyIGWAttachmentXML{{VpcID: e.AttachedVPCID, State: e.State}},
		Tags:                        toTagItems(e.Tags),
	}
}

func writeEgressOnlyErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidEgressOnlyInternetGatewayId.NotFound", "DependencyViolation")
}
