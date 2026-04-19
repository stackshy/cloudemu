package ec2

import (
	"encoding/xml"
	"net/http"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type igwAttachmentXML struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type internetGatewayXML struct {
	InternetGatewayID string             `xml:"internetGatewayId"`
	Attachments       []igwAttachmentXML `xml:"attachmentSet>item,omitempty"`
	Tags              []tagItem          `xml:"tagSet>item,omitempty"`
}

type createInternetGatewayResponseXML struct {
	XMLName         xml.Name           `xml:"CreateInternetGatewayResponse"`
	Xmlns           string             `xml:"xmlns,attr"`
	RequestID       string             `xml:"requestId"`
	InternetGateway internetGatewayXML `xml:"internetGateway"`
}

type describeInternetGatewaysResponseXML struct {
	XMLName            xml.Name             `xml:"DescribeInternetGatewaysResponse"`
	Xmlns              string               `xml:"xmlns,attr"`
	RequestID          string               `xml:"requestId"`
	InternetGatewaySet []internetGatewayXML `xml:"internetGatewaySet>item"`
}

type attachInternetGatewayResponseXML struct {
	XMLName   xml.Name `xml:"AttachInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type detachInternetGatewayResponseXML struct {
	XMLName   xml.Name `xml:"DetachInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createInternetGateway(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.InternetGatewayConfig{
		Tags: mergeTagSpecs(awsquery.TagSpecs(r.Form), "internet-gateway"),
	}

	igw, err := h.vpc.CreateInternetGateway(r.Context(), cfg)
	if err != nil {
		writeIGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createInternetGatewayResponseXML{
		Xmlns:           awsquery.Namespace,
		RequestID:       awsquery.RequestID,
		InternetGateway: toInternetGatewayXML(igw),
	})
}

func (h *Handler) attachInternetGateway(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.AttachInternetGateway(r.Context(),
		r.Form.Get("InternetGatewayId"), r.Form.Get("VpcId")); err != nil {
		writeIGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, attachInternetGatewayResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) detachInternetGateway(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DetachInternetGateway(r.Context(),
		r.Form.Get("InternetGatewayId"), r.Form.Get("VpcId")); err != nil {
		writeIGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, detachInternetGatewayResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/sg/route_table
func (h *Handler) describeInternetGateways(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InternetGatewayId")

	igws, err := h.vpc.DescribeInternetGateways(r.Context(), ids)
	if err != nil {
		writeIGWErr(w, err)
		return
	}

	out := make([]internetGatewayXML, 0, len(igws))
	for i := range igws {
		out = append(out, toInternetGatewayXML(&igws[i]))
	}

	awsquery.WriteXMLResponse(w, describeInternetGatewaysResponseXML{
		Xmlns:              awsquery.Namespace,
		RequestID:          awsquery.RequestID,
		InternetGatewaySet: out,
	})
}

func toInternetGatewayXML(igw *netdriver.InternetGateway) internetGatewayXML {
	xi := internetGatewayXML{
		InternetGatewayID: igw.ID,
		Tags:              toTagItems(igw.Tags),
	}

	if igw.VpcID != "" {
		state := igw.State
		if state == "" {
			state = "attached"
		}

		xi.Attachments = []igwAttachmentXML{{VpcID: igw.VpcID, State: state}}
	}

	return xi
}

func writeIGWErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidInternetGatewayID.NotFound", "DependencyViolation")
}
