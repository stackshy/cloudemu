package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// stateAttached is the attachment state shared by internet gateways and
// network interfaces.
const stateAttached = "attached"

type igwAttachmentXML struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type internetGatewayXML struct {
	InternetGatewayID string             `xml:"internetGatewayId"`
	OwnerID           string             `xml:"ownerId"`
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

type deleteInternetGatewayResponseXML struct {
	XMLName   xml.Name `xml:"DeleteInternetGatewayResponse"`
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

func (h *Handler) deleteInternetGateway(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteInternetGateway(r.Context(), r.Form.Get("InternetGatewayId")); err != nil {
		writeIGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteInternetGatewayResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe+filter pattern; sibling in vpc
func (h *Handler) describeInternetGateways(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InternetGatewayId")

	igws, err := h.vpc.DescribeInternetGateways(r.Context(), ids)
	if err != nil {
		writeIGWErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateIGWFilters(filters); err != nil {
		writeIGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, describeInternetGatewaysResponseXML{
		Xmlns:              awsquery.Namespace,
		RequestID:          awsquery.RequestID,
		InternetGatewaySet: filterXML(igws, filters, igwMatchesFilters, toInternetGatewayXML),
	})
}

// validateIGWFilters rejects filter names DescribeInternetGateways does not
// model, matching the sibling Describe handlers.
func validateIGWFilters(filters []awsquery.Filter) error {
	var probe netdriver.InternetGateway

	for _, f := range filters {
		if _, known := igwFilterMatch(&probe, f); !known {
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

func igwMatchesFilters(igw *netdriver.InternetGateway, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if matched, _ := igwFilterMatch(igw, f); !matched {
			return false
		}
	}

	return true
}

// igwFilterMatch reports whether igw satisfies filter f and whether f is a
// filter DescribeInternetGateways recognizes.
func igwFilterMatch(igw *netdriver.InternetGateway, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case "internet-gateway-id":
		return containsString(f.Values, igw.ID), true
	case "attachment.vpc-id":
		return igw.VpcID != "" && containsString(f.Values, igw.VpcID), true
	case "attachment.state":
		return igw.VpcID != "" && containsString(f.Values, nonEmpty(igw.State, stateAttached)), true
	default:
		return tagFilterMatch(f.Name, f.Values, igw.Tags)
	}
}

func toInternetGatewayXML(igw *netdriver.InternetGateway) internetGatewayXML {
	xi := internetGatewayXML{
		InternetGatewayID: igw.ID,
		OwnerID:           ownerID,
		Tags:              toTagItems(igw.Tags),
	}

	if igw.VpcID != "" {
		state := igw.State
		if state == "" {
			state = stateAttached
		}

		xi.Attachments = []igwAttachmentXML{{VpcID: igw.VpcID, State: state}}
	}

	return xi
}

func writeIGWErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidInternetGatewayID.NotFound", "DependencyViolation")
}
