package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) ipamDiscovery() (netdriver.IPAMDiscovery, bool) {
	i, ok := h.vpc.(netdriver.IPAMDiscovery)

	return i, ok
}

type ipamRDXML struct {
	IpamResourceDiscoveryID     string     `xml:"ipamResourceDiscoveryId"`
	IpamResourceDiscoveryArn    string     `xml:"ipamResourceDiscoveryArn"`
	IpamResourceDiscoveryRegion string     `xml:"ipamResourceDiscoveryRegion,omitempty"`
	OwnerID                     string     `xml:"ownerId,omitempty"`
	OperatingRegions            []opRegXML `xml:"operatingRegionSet>item,omitempty"`
	Description                 string     `xml:"description,omitempty"`
	IsDefault                   bool       `xml:"isDefault"`
	State                       string     `xml:"state"`
	Tags                        []tagItem  `xml:"tagSet>item,omitempty"`
}

type ipamRDAssocXML struct {
	IpamResourceDiscoveryAssociationID  string    `xml:"ipamResourceDiscoveryAssociationId"`
	IpamResourceDiscoveryAssociationArn string    `xml:"ipamResourceDiscoveryAssociationArn"`
	IpamID                              string    `xml:"ipamId"`
	IpamArn                             string    `xml:"ipamArn,omitempty"`
	IpamRegion                          string    `xml:"ipamRegion,omitempty"`
	IpamResourceDiscoveryID             string    `xml:"ipamResourceDiscoveryId"`
	OwnerID                             string    `xml:"ownerId,omitempty"`
	IsDefault                           bool      `xml:"isDefault"`
	ResourceDiscoveryStatus             string    `xml:"resourceDiscoveryStatus,omitempty"`
	State                               string    `xml:"state"`
	Tags                                []tagItem `xml:"tagSet>item,omitempty"`
}

//nolint:gocyclo // flat action dispatch table
func (h *Handler) routeIPAMDiscovery(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, ok := h.ipamDiscovery()
	if !ok {
		return false
	}

	switch action {
	case "CreateIpamResourceDiscovery":
		h.createIpamRD(w, r, ip)
	case "DescribeIpamResourceDiscoveries":
		h.describeIpamRDs(w, r, ip)
	case "ModifyIpamResourceDiscovery":
		h.modifyIpamRD(w, r, ip)
	case "DeleteIpamResourceDiscovery":
		h.deleteIpamRD(w, r, ip)
	case "AssociateIpamResourceDiscovery":
		h.associateIpamRD(w, r, ip)
	case "DisassociateIpamResourceDiscovery":
		h.disassociateIpamRD(w, r, ip)
	case "DescribeIpamResourceDiscoveryAssociations":
		h.describeIpamRDAssocs(w, r, ip)
	case "GetIpamDiscoveredAccounts":
		h.getIpamDiscoveredAccounts(w, r, ip)
	case "GetIpamDiscoveredResourceCidrs":
		h.getIpamDiscoveredResourceCidrs(w, r, ip)
	case "GetIpamDiscoveredPublicAddresses":
		h.getIpamDiscoveredPublicAddresses(w, r, ip)
	default:
		return false
	}

	return true
}

func (*Handler) createIpamRD(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	out, err := ip.CreateIpamResourceDiscovery(r.Context(), netdriver.IpamResourceDiscoveryConfig{
		Description: r.Form.Get("Description"),
		Tags:        mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-resource-discovery"),
	})
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamRD(w, "CreateIpamResourceDiscoveryResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamRDs(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	items, err := ip.DescribeIpamResourceDiscoveries(r.Context(), awsquery.ListStrings(r.Form, "IpamResourceDiscoveryId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamRDXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamRDXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name    `xml:"DescribeIpamResourceDiscoveriesResponse"`
		Xmlns   string      `xml:"xmlns,attr"`
		Req     string      `xml:"requestId"`
		Set     []ipamRDXML `xml:"ipamResourceDiscoverySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamRD(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	out, err := ip.ModifyIpamResourceDiscovery(r.Context(),
		r.Form.Get("IpamResourceDiscoveryId"), r.Form.Get("Description"), awsquery.ListStrings(r.Form, "AddOperatingRegion"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamRD(w, "ModifyIpamResourceDiscoveryResponse", out)
}

func (*Handler) deleteIpamRD(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	out, err := ip.DeleteIpamResourceDiscovery(r.Context(), r.Form.Get("IpamResourceDiscoveryId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamRD(w, "DeleteIpamResourceDiscoveryResponse", out)
}

func writeIpamRD(w http.ResponseWriter, root string, out *netdriver.IpamResourceDiscovery) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name  `xml:""`
		Xmlns   string    `xml:"xmlns,attr"`
		Req     string    `xml:"requestId"`
		RD      ipamRDXML `xml:"ipamResourceDiscovery"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, RD: toIpamRDXML(out)})
}

func toIpamRDXML(rd *netdriver.IpamResourceDiscovery) ipamRDXML {
	x := ipamRDXML{
		IpamResourceDiscoveryID: rd.ID, IpamResourceDiscoveryArn: rd.ARN, IpamResourceDiscoveryRegion: rd.Region,
		OwnerID: rd.OwnerID, Description: rd.Description, IsDefault: rd.IsDefault, State: rd.State,
		Tags: toTagItems(rd.Tags),
	}

	for _, reg := range rd.OperatingRegions {
		x.OperatingRegions = append(x.OperatingRegions, opRegXML{RegionName: reg})
	}

	return x
}

func (*Handler) associateIpamRD(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	out, err := ip.AssociateIpamResourceDiscovery(r.Context(),
		r.Form.Get("IpamId"), r.Form.Get("IpamResourceDiscoveryId"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-resource-discovery-association"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamRDAssoc(w, "AssociateIpamResourceDiscoveryResponse", out)
}

func (*Handler) disassociateIpamRD(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	out, err := ip.DisassociateIpamResourceDiscovery(r.Context(), r.Form.Get("IpamResourceDiscoveryAssociationId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamRDAssoc(w, "DisassociateIpamResourceDiscoveryResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamRDAssocs(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	items, err := ip.DescribeIpamResourceDiscoveryAssociations(r.Context(), awsquery.ListStrings(r.Form, "IpamResourceDiscoveryAssociationId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamRDAssocXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamRDAssocXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"DescribeIpamResourceDiscoveryAssociationsResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		Set     []ipamRDAssocXML `xml:"ipamResourceDiscoveryAssociationSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func writeIpamRDAssoc(w http.ResponseWriter, root string, out *netdriver.IpamResourceDiscoveryAssociation) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name       `xml:""`
		Xmlns   string         `xml:"xmlns,attr"`
		Req     string         `xml:"requestId"`
		Assoc   ipamRDAssocXML `xml:"ipamResourceDiscoveryAssociation"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Assoc: toIpamRDAssocXML(out)})
}

func toIpamRDAssocXML(a *netdriver.IpamResourceDiscoveryAssociation) ipamRDAssocXML {
	return ipamRDAssocXML{
		IpamResourceDiscoveryAssociationID: a.ID, IpamResourceDiscoveryAssociationArn: a.ARN,
		IpamID: a.IpamID, IpamArn: a.IpamARN, IpamRegion: a.IpamRegion, IpamResourceDiscoveryID: a.ResourceDiscoveryID,
		OwnerID: a.OwnerID, IsDefault: a.IsDefault, ResourceDiscoveryStatus: a.ResourceDiscoveryStatus, State: a.State,
		Tags: toTagItems(a.Tags),
	}
}

func (*Handler) getIpamDiscoveredAccounts(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	items, err := ip.GetIpamDiscoveredAccounts(r.Context(), r.Form.Get("IpamResourceDiscoveryId"), r.Form.Get("DiscoveryRegion"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type accXML struct {
		AccountID       string `xml:"accountId"`
		DiscoveryRegion string `xml:"discoveryRegion"`
	}

	out := make([]accXML, 0, len(items))
	for i := range items {
		out = append(out, accXML{AccountID: items[i].AccountID, DiscoveryRegion: items[i].DiscoveryRegion})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"GetIpamDiscoveredAccountsResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Set     []accXML `xml:"ipamDiscoveredAccountSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getIpamDiscoveredResourceCidrs(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	items, err := ip.GetIpamDiscoveredResourceCidrs(r.Context(), r.Form.Get("IpamResourceDiscoveryId"), r.Form.Get("ResourceRegion"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type drcXML struct {
		IpamResourceDiscoveryID string  `xml:"ipamResourceDiscoveryId"`
		ResourceCidr            string  `xml:"resourceCidr"`
		ResourceID              string  `xml:"resourceId"`
		ResourceType            string  `xml:"resourceType"`
		ResourceRegion          string  `xml:"resourceRegion,omitempty"`
		ResourceOwnerID         string  `xml:"resourceOwnerId,omitempty"`
		VpcID                   string  `xml:"vpcId,omitempty"`
		IPSource                string  `xml:"ipSource,omitempty"`
		IPUsage                 float64 `xml:"ipUsage,omitempty"`
		SampleTime              string  `xml:"sampleTime,omitempty"`
	}

	out := make([]drcXML, 0, len(items))
	for i := range items {
		out = append(out, drcXML{
			IpamResourceDiscoveryID: items[i].ResourceDiscoveryID, ResourceCidr: items[i].ResourceCIDR,
			ResourceID: items[i].ResourceID, ResourceType: items[i].ResourceType, ResourceRegion: items[i].ResourceRegion,
			ResourceOwnerID: items[i].ResourceOwnerID, VpcID: items[i].VPCID, IPSource: items[i].IPSource,
			IPUsage: items[i].IPUsage, SampleTime: items[i].SampleTime.Format("2006-01-02T15:04:05Z"),
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"GetIpamDiscoveredResourceCidrsResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Set     []drcXML `xml:"ipamDiscoveredResourceCidrSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getIpamDiscoveredPublicAddresses(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMDiscovery) {
	items, err := ip.GetIpamDiscoveredPublicAddresses(r.Context(), r.Form.Get("IpamResourceDiscoveryId"), r.Form.Get("AddressRegion"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type paXML struct {
		IpamResourceDiscoveryID string `xml:"ipamResourceDiscoveryId"`
		Address                 string `xml:"address"`
		AddressAllocationID     string `xml:"addressAllocationId,omitempty"`
		AddressOwnerID          string `xml:"addressOwnerId,omitempty"`
		AddressRegion           string `xml:"addressRegion,omitempty"`
		AddressType             string `xml:"addressType,omitempty"`
		AssociationStatus       string `xml:"associationStatus,omitempty"`
		Service                 string `xml:"service,omitempty"`
		SampleTime              string `xml:"sampleTime,omitempty"`
	}

	out := make([]paXML, 0, len(items))
	for i := range items {
		out = append(out, paXML{
			IpamResourceDiscoveryID: items[i].ResourceDiscoveryID, Address: items[i].Address,
			AddressAllocationID: items[i].AddressAllocationID, AddressOwnerID: items[i].AddressOwnerID,
			AddressRegion: items[i].AddressRegion, AddressType: items[i].AddressType,
			AssociationStatus: items[i].AssociationStatus, Service: items[i].Service,
			SampleTime: items[i].SampleTime.Format("2006-01-02T15:04:05Z"),
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName      xml.Name `xml:"GetIpamDiscoveredPublicAddressesResponse"`
		Xmlns        string   `xml:"xmlns,attr"`
		Req          string   `xml:"requestId"`
		Set          []paXML  `xml:"ipamDiscoveredPublicAddressSet>item"`
		OldestSample string   `xml:"oldestSampleTime,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}
