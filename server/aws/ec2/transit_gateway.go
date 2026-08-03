package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// transitGateways reports whether the driver models transit gateways (optional).
func (h *Handler) transitGateways() (netdriver.TransitGateways, bool) {
	tg, ok := h.vpc.(netdriver.TransitGateways)

	return tg, ok
}

type tgwOptionsXML struct {
	AmazonSideASN int64 `xml:"amazonSideAsn"`
}

type transitGatewayXML struct {
	TransitGatewayID string        `xml:"transitGatewayId"`
	State            string        `xml:"state"`
	OwnerID          string        `xml:"ownerId,omitempty"`
	Description      string        `xml:"description,omitempty"`
	Options          tgwOptionsXML `xml:"options"`
	Tags             []tagItem     `xml:"tagSet>item,omitempty"`
}

type transitGatewayAttachmentXML struct {
	TransitGatewayAttachmentID string    `xml:"transitGatewayAttachmentId"`
	TransitGatewayID           string    `xml:"transitGatewayId"`
	VpcID                      string    `xml:"vpcId"`
	SubnetIDs                  []string  `xml:"subnetIds>item,omitempty"`
	State                      string    `xml:"state"`
	Tags                       []tagItem `xml:"tagSet>item,omitempty"`
}

type transitGatewayRouteTableXML struct {
	TransitGatewayRouteTableID string    `xml:"transitGatewayRouteTableId"`
	TransitGatewayID           string    `xml:"transitGatewayId"`
	State                      string    `xml:"state"`
	Tags                       []tagItem `xml:"tagSet>item,omitempty"`
}

func (h *Handler) routeTransitGateways(w http.ResponseWriter, r *http.Request, action string) bool {
	tg, ok := h.transitGateways()
	if !ok {
		return false
	}

	switch action {
	case "CreateTransitGateway":
		h.createTransitGateway(w, r, tg)
	case "DeleteTransitGateway":
		h.deleteTransitGateway(w, r, tg)
	case "DescribeTransitGateways":
		h.describeTransitGateways(w, r, tg)
	case "CreateTransitGatewayVpcAttachment":
		h.createTGWAttachment(w, r, tg)
	case "DeleteTransitGatewayVpcAttachment":
		h.deleteTGWAttachment(w, r, tg)
	case "DescribeTransitGatewayVpcAttachments":
		h.describeTGWAttachments(w, r, tg)
	case "CreateTransitGatewayRouteTable":
		h.createTGWRouteTable(w, r, tg)
	case "DeleteTransitGatewayRouteTable":
		h.deleteTGWRouteTable(w, r, tg)
	case "DescribeTransitGatewayRouteTables":
		h.describeTGWRouteTables(w, r, tg)
	default:
		return false
	}

	return true
}

func (h *Handler) createTransitGateway(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	asn, _ := strconv.ParseInt(r.Form.Get("Options.AmazonSideAsn"), 10, 64)
	out, err := tg.CreateTransitGateway(r.Context(), netdriver.TransitGatewayConfig{
		ASN:         asn,
		Description: r.Form.Get("Description"),
		Tags:        mergeTagSpecs(awsquery.TagSpecs(r.Form), "transit-gateway"),
	})
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName        xml.Name          `xml:"CreateTransitGatewayResponse"`
		Xmlns          string            `xml:"xmlns,attr"`
		RequestID      string            `xml:"requestId"`
		TransitGateway transitGatewayXML `xml:"transitGateway"`
	}{Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, TransitGateway: toTGWXML(out)})
}

func (h *Handler) deleteTransitGateway(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	out, err := tg.DeleteTransitGateway(r.Context(), r.Form.Get("TransitGatewayId"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName        xml.Name          `xml:"DeleteTransitGatewayResponse"`
		Xmlns          string            `xml:"xmlns,attr"`
		RequestID      string            `xml:"requestId"`
		TransitGateway transitGatewayXML `xml:"transitGateway"`
	}{Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, TransitGateway: toTGWXML(out)})
}

func (h *Handler) describeTransitGateways(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	items, err := tg.DescribeTransitGateways(r.Context(), awsquery.ListStrings(r.Form, "TransitGatewayIds"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	out := make([]transitGatewayXML, 0, len(items))
	for i := range items {
		out = append(out, toTGWXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name            `xml:"DescribeTransitGatewaysResponse"`
		Xmlns   string              `xml:"xmlns,attr"`
		Req     string              `xml:"requestId"`
		Set     []transitGatewayXML `xml:"transitGatewaySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (h *Handler) createTGWAttachment(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	out, err := tg.CreateTransitGatewayVPCAttachment(r.Context(), netdriver.TransitGatewayVPCAttachmentConfig{
		TransitGatewayID: r.Form.Get("TransitGatewayId"),
		VPCID:            r.Form.Get("VpcId"),
		SubnetIDs:        awsquery.ListStrings(r.Form, "SubnetIds"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "transit-gateway-attachment"),
	})
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name                    `xml:"CreateTransitGatewayVpcAttachmentResponse"`
		Xmlns      string                      `xml:"xmlns,attr"`
		RequestID  string                      `xml:"requestId"`
		Attachment transitGatewayAttachmentXML `xml:"transitGatewayVpcAttachment"`
	}{Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, Attachment: toTGWAttachmentXML(out)})
}

func (h *Handler) deleteTGWAttachment(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	out, err := tg.DeleteTransitGatewayVPCAttachment(r.Context(), r.Form.Get("TransitGatewayAttachmentId"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name                    `xml:"DeleteTransitGatewayVpcAttachmentResponse"`
		Xmlns      string                      `xml:"xmlns,attr"`
		RequestID  string                      `xml:"requestId"`
		Attachment transitGatewayAttachmentXML `xml:"transitGatewayVpcAttachment"`
	}{Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, Attachment: toTGWAttachmentXML(out)})
}

func (h *Handler) describeTGWAttachments(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	items, err := tg.DescribeTransitGatewayVPCAttachments(r.Context(), awsquery.ListStrings(r.Form, "TransitGatewayAttachmentIds"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	out := make([]transitGatewayAttachmentXML, 0, len(items))
	for i := range items {
		out = append(out, toTGWAttachmentXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                      `xml:"DescribeTransitGatewayVpcAttachmentsResponse"`
		Xmlns   string                        `xml:"xmlns,attr"`
		Req     string                        `xml:"requestId"`
		Set     []transitGatewayAttachmentXML `xml:"transitGatewayVpcAttachments>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (h *Handler) createTGWRouteTable(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	out, err := tg.CreateTransitGatewayRouteTable(r.Context(), r.Form.Get("TransitGatewayId"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "transit-gateway-route-table"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name                    `xml:"CreateTransitGatewayRouteTableResponse"`
		Xmlns      string                      `xml:"xmlns,attr"`
		RequestID  string                      `xml:"requestId"`
		RouteTable transitGatewayRouteTableXML `xml:"transitGatewayRouteTable"`
	}{Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, RouteTable: toTGWRouteTableXML(out)})
}

func (h *Handler) deleteTGWRouteTable(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	out, err := tg.DeleteTransitGatewayRouteTable(r.Context(), r.Form.Get("TransitGatewayRouteTableId"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name                    `xml:"DeleteTransitGatewayRouteTableResponse"`
		Xmlns      string                      `xml:"xmlns,attr"`
		RequestID  string                      `xml:"requestId"`
		RouteTable transitGatewayRouteTableXML `xml:"transitGatewayRouteTable"`
	}{Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, RouteTable: toTGWRouteTableXML(out)})
}

func (h *Handler) describeTGWRouteTables(w http.ResponseWriter, r *http.Request, tg netdriver.TransitGateways) {
	items, err := tg.DescribeTransitGatewayRouteTables(r.Context(), awsquery.ListStrings(r.Form, "TransitGatewayRouteTableIds"))
	if err != nil {
		writeTGWErr(w, err)
		return
	}

	out := make([]transitGatewayRouteTableXML, 0, len(items))
	for i := range items {
		out = append(out, toTGWRouteTableXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                      `xml:"DescribeTransitGatewayRouteTablesResponse"`
		Xmlns   string                        `xml:"xmlns,attr"`
		Req     string                        `xml:"requestId"`
		Set     []transitGatewayRouteTableXML `xml:"transitGatewayRouteTables>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func toTGWXML(t *netdriver.TransitGateway) transitGatewayXML {
	return transitGatewayXML{
		TransitGatewayID: t.ID,
		State:            t.State,
		OwnerID:          t.OwnerID,
		Description:      t.Description,
		Options:          tgwOptionsXML{AmazonSideASN: t.ASN},
		Tags:             toTagItems(t.Tags),
	}
}

func toTGWAttachmentXML(a *netdriver.TransitGatewayVPCAttachment) transitGatewayAttachmentXML {
	return transitGatewayAttachmentXML{
		TransitGatewayAttachmentID: a.ID,
		TransitGatewayID:           a.TransitGatewayID,
		VpcID:                      a.VPCID,
		SubnetIDs:                  a.SubnetIDs,
		State:                      a.State,
		Tags:                       toTagItems(a.Tags),
	}
}

func toTGWRouteTableXML(t *netdriver.TransitGatewayRouteTable) transitGatewayRouteTableXML {
	return transitGatewayRouteTableXML{
		TransitGatewayRouteTableID: t.ID,
		TransitGatewayID:           t.TransitGatewayID,
		State:                      t.State,
		Tags:                       toTagItems(t.Tags),
	}
}

func writeTGWErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidTransitGatewayID.NotFound", "IncorrectState")
}
