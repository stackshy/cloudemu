package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) ipamByoasn() (netdriver.IPAMByoasn, bool) {
	i, ok := h.vpc.(netdriver.IPAMByoasn)

	return i, ok
}

func (h *Handler) ipamByoip() (netdriver.IPAMByoip, bool) {
	i, ok := h.vpc.(netdriver.IPAMByoip)

	return i, ok
}

type byoasnXML struct {
	Asn           string `xml:"asn"`
	IpamID        string `xml:"ipamId,omitempty"`
	State         string `xml:"state"`
	StatusMessage string `xml:"statusMessage,omitempty"`
}

type asnAssociationXML struct {
	Asn           string `xml:"asn"`
	Cidr          string `xml:"cidr"`
	State         string `xml:"state"`
	StatusMessage string `xml:"statusMessage,omitempty"`
}

type byoipCidrXML struct {
	Cidr               string              `xml:"cidr"`
	Description        string              `xml:"description,omitempty"`
	State              string              `xml:"state"`
	StatusMessage      string              `xml:"statusMessage,omitempty"`
	NetworkBorderGroup string              `xml:"networkBorderGroup,omitempty"`
	AdvertisementType  string              `xml:"advertisementType,omitempty"`
	AsnAssociations    []asnAssociationXML `xml:"asnAssociationSet>item,omitempty"`
}

func (h *Handler) routeIPAMByoip(w http.ResponseWriter, r *http.Request, action string) bool {
	handledAsn := h.routeIPAMByoasn(w, r, action)
	if handledAsn {
		return true
	}

	ip, ok := h.ipamByoip()
	if !ok {
		return false
	}

	switch action {
	case "ProvisionByoipCidr":
		h.provisionByoipCidr(w, r, ip)
	case "DeprovisionByoipCidr":
		h.deprovisionByoipCidr(w, r, ip)
	case "MoveByoipCidrToIpam":
		h.moveByoipCidrToIpam(w, r, ip)
	case "DescribeByoipCidrs":
		h.describeByoipCidrs(w, r, ip)
	case "AdvertiseByoipCidr":
		h.advertiseByoipCidr(w, r, ip)
	case "WithdrawByoipCidr":
		h.withdrawByoipCidr(w, r, ip)
	default:
		return false
	}

	return true
}

func (h *Handler) routeIPAMByoasn(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, ok := h.ipamByoasn()
	if !ok {
		return false
	}

	switch action {
	case "ProvisionIpamByoasn":
		h.provisionIpamByoasn(w, r, ip)
	case "DeprovisionIpamByoasn":
		h.deprovisionIpamByoasn(w, r, ip)
	case "DescribeIpamByoasn":
		h.describeIpamByoasn(w, r, ip)
	case "AssociateIpamByoasn":
		h.associateIpamByoasn(w, r, ip)
	case "DisassociateIpamByoasn":
		h.disassociateIpamByoasn(w, r, ip)
	default:
		return false
	}

	return true
}

func (*Handler) provisionIpamByoasn(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoasn) {
	out, err := ip.ProvisionIpamByoasn(r.Context(), r.Form.Get("IpamId"), r.Form.Get("Asn"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoasn(w, "ProvisionIpamByoasnResponse", out)
}

func (*Handler) deprovisionIpamByoasn(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoasn) {
	out, err := ip.DeprovisionIpamByoasn(r.Context(), r.Form.Get("IpamId"), r.Form.Get("Asn"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoasn(w, "DeprovisionIpamByoasnResponse", out)
}

func (*Handler) describeIpamByoasn(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoasn) {
	items, err := ip.DescribeIpamByoasn(r.Context())
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]byoasnXML, 0, len(items))
	for i := range items {
		out = append(out, byoasnXML{Asn: items[i].Asn, IpamID: items[i].IpamID, State: items[i].State, StatusMessage: items[i].StatusMessage})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name    `xml:"DescribeIpamByoasnResponse"`
		Xmlns   string      `xml:"xmlns,attr"`
		Req     string      `xml:"requestId"`
		Set     []byoasnXML `xml:"byoasnSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) associateIpamByoasn(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoasn) {
	out, err := ip.AssociateIpamByoasn(r.Context(), r.Form.Get("Asn"), r.Form.Get("Cidr"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeAsnAssociation(w, "AssociateIpamByoasnResponse", out)
}

func (*Handler) disassociateIpamByoasn(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoasn) {
	out, err := ip.DisassociateIpamByoasn(r.Context(), r.Form.Get("Asn"), r.Form.Get("Cidr"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeAsnAssociation(w, "DisassociateIpamByoasnResponse", out)
}

func writeByoasn(w http.ResponseWriter, root string, out *netdriver.Byoasn) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name  `xml:""`
		Xmlns   string    `xml:"xmlns,attr"`
		Req     string    `xml:"requestId"`
		Byoasn  byoasnXML `xml:"byoasn"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Byoasn: byoasnXML{
		Asn: out.Asn, IpamID: out.IpamID, State: out.State, StatusMessage: out.StatusMessage,
	}})
}

func writeAsnAssociation(w http.ResponseWriter, root string, out *netdriver.AsnAssociation) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name          `xml:""`
		Xmlns   string            `xml:"xmlns,attr"`
		Req     string            `xml:"requestId"`
		Assoc   asnAssociationXML `xml:"asnAssociation"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Assoc: asnAssociationXML{
		Asn: out.Asn, Cidr: out.CIDR, State: out.State, StatusMessage: out.StatusMessage,
	}})
}

func (*Handler) provisionByoipCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoip) {
	out, err := ip.ProvisionByoipCidr(r.Context(), r.Form.Get("Cidr"), r.Form.Get("Description"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoipCidr(w, "ProvisionByoipCidrResponse", out)
}

func (*Handler) deprovisionByoipCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoip) {
	out, err := ip.DeprovisionByoipCidr(r.Context(), r.Form.Get("Cidr"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoipCidr(w, "DeprovisionByoipCidrResponse", out)
}

func (*Handler) moveByoipCidrToIpam(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoip) {
	out, err := ip.MoveByoipCidrToIpam(r.Context(), r.Form.Get("Cidr"), r.Form.Get("IpamPoolId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoipCidr(w, "MoveByoipCidrToIpamResponse", out)
}

func (*Handler) describeByoipCidrs(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoip) {
	items, err := ip.DescribeByoipCidrs(r.Context())
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]byoipCidrXML, 0, len(items))
	for i := range items {
		out = append(out, toByoipCidrXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name       `xml:"DescribeByoipCidrsResponse"`
		Xmlns   string         `xml:"xmlns,attr"`
		Req     string         `xml:"requestId"`
		Set     []byoipCidrXML `xml:"byoipCidrSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) advertiseByoipCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoip) {
	out, err := ip.AdvertiseByoipCidr(r.Context(), r.Form.Get("Cidr"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoipCidr(w, "AdvertiseByoipCidrResponse", out)
}

func (*Handler) withdrawByoipCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMByoip) {
	out, err := ip.WithdrawByoipCidr(r.Context(), r.Form.Get("Cidr"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeByoipCidr(w, "WithdrawByoipCidrResponse", out)
}

func writeByoipCidr(w http.ResponseWriter, root string, out *netdriver.ByoipCidr) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name     `xml:""`
		Xmlns   string       `xml:"xmlns,attr"`
		Req     string       `xml:"requestId"`
		Cidr    byoipCidrXML `xml:"byoipCidr"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Cidr: toByoipCidrXML(out)})
}

func toByoipCidrXML(bc *netdriver.ByoipCidr) byoipCidrXML {
	x := byoipCidrXML{
		Cidr: bc.CIDR, Description: bc.Description, State: bc.State, StatusMessage: bc.StatusMessage,
		NetworkBorderGroup: bc.NetworkBorderGroup, AdvertisementType: bc.AdvertisementType,
	}

	for _, a := range bc.AsnAssociations {
		x.AsnAssociations = append(x.AsnAssociations, asnAssociationXML{Asn: a.Asn, Cidr: a.CIDR, State: a.State, StatusMessage: a.StatusMessage})
	}

	return x
}
