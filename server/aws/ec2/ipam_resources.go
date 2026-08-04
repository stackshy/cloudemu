package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) ipamResources() (netdriver.IPAMResources, bool) {
	i, ok := h.vpc.(netdriver.IPAMResources)

	return i, ok
}

type ipamResourceCidrXML struct {
	IpamID           string    `xml:"ipamId,omitempty"`
	IpamScopeID      string    `xml:"ipamScopeId,omitempty"`
	IpamPoolID       string    `xml:"ipamPoolId,omitempty"`
	ResourceCidr     string    `xml:"resourceCidr"`
	ResourceID       string    `xml:"resourceId"`
	ResourceName     string    `xml:"resourceName,omitempty"`
	ResourceType     string    `xml:"resourceType"`
	ResourceRegion   string    `xml:"resourceRegion,omitempty"`
	ResourceOwnerID  string    `xml:"resourceOwnerId,omitempty"`
	VpcID            string    `xml:"vpcId,omitempty"`
	AvailabilityZone string    `xml:"availabilityZoneId,omitempty"`
	ComplianceStatus string    `xml:"complianceStatus,omitempty"`
	ManagementState  string    `xml:"managementState,omitempty"`
	OverlapStatus    string    `xml:"overlapStatus,omitempty"`
	IPUsage          float64   `xml:"ipUsage,omitempty"`
	Tags             []tagItem `xml:"resourceTagSet>item,omitempty"`
}

type ipamHistoryRecordXML struct {
	ResourceCidr             string `xml:"resourceCidr"`
	ResourceID               string `xml:"resourceId"`
	ResourceType             string `xml:"resourceType"`
	ResourceRegion           string `xml:"resourceRegion,omitempty"`
	ResourceOwnerID          string `xml:"resourceOwnerId,omitempty"`
	VpcID                    string `xml:"vpcId,omitempty"`
	ResourceComplianceStatus string `xml:"resourceComplianceStatus,omitempty"`
	ResourceOverlapStatus    string `xml:"resourceOverlapStatus,omitempty"`
	SampledStartTime         string `xml:"sampledStartTime,omitempty"`
	SampledEndTime           string `xml:"sampledEndTime,omitempty"`
}

func (h *Handler) routeIPAMResources(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, ok := h.ipamResources()
	if !ok {
		return false
	}

	switch action {
	case "GetIpamResourceCidrs":
		h.getIpamResourceCidrs(w, r, ip)
	case "ModifyIpamResourceCidr":
		h.modifyIpamResourceCidr(w, r, ip)
	case "GetIpamAddressHistory":
		h.getIpamAddressHistory(w, r, ip)
	default:
		return false
	}

	return true
}

func (*Handler) getIpamResourceCidrs(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMResources) {
	items, err := ip.GetIpamResourceCidrs(r.Context(), r.Form.Get("IpamScopeId"), r.Form.Get("ResourceId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamResourceCidrXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamResourceCidrXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name              `xml:"GetIpamResourceCidrsResponse"`
		Xmlns   string                `xml:"xmlns,attr"`
		Req     string                `xml:"requestId"`
		Set     []ipamResourceCidrXML `xml:"ipamResourceCidrSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamResourceCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMResources) {
	out, err := ip.ModifyIpamResourceCidr(r.Context(),
		r.Form.Get("ResourceId"), r.Form.Get("CurrentIpamScopeId"), r.Form.Get("DestinationIpamScopeId"),
		r.Form.Get("Monitored") == formTrue)
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	x := toIpamResourceCidrXML(out)

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name            `xml:"ModifyIpamResourceCidrResponse"`
		Xmlns   string              `xml:"xmlns,attr"`
		Req     string              `xml:"requestId"`
		Cidr    ipamResourceCidrXML `xml:"ipamResourceCidr"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Cidr: x})
}

func (*Handler) getIpamAddressHistory(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMResources) {
	items, err := ip.GetIpamAddressHistory(r.Context(), r.Form.Get("Cidr"), r.Form.Get("IpamScopeId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamHistoryRecordXML, 0, len(items))
	for i := range items {
		out = append(out, ipamHistoryRecordXML{
			ResourceCidr: items[i].ResourceCIDR, ResourceID: items[i].ResourceID, ResourceType: items[i].ResourceType,
			ResourceRegion: items[i].ResourceRegion, ResourceOwnerID: items[i].ResourceOwnerID, VpcID: items[i].VPCID,
			ResourceComplianceStatus: items[i].ResourceComplianceStatus, ResourceOverlapStatus: items[i].ResourceOverlapStatus,
			SampledStartTime: items[i].SampledStartTime.Format("2006-01-02T15:04:05Z"),
			SampledEndTime:   items[i].SampledEndTime.Format("2006-01-02T15:04:05Z"),
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"GetIpamAddressHistoryResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Set     []ipamHistoryRecordXML `xml:"historyRecordSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func toIpamResourceCidrXML(c *netdriver.IpamResourceCidr) ipamResourceCidrXML {
	return ipamResourceCidrXML{
		IpamID: c.IpamID, IpamScopeID: c.IpamScopeID, IpamPoolID: c.IpamPoolID,
		ResourceCidr: c.ResourceCIDR, ResourceID: c.ResourceID, ResourceName: c.ResourceName,
		ResourceType: c.ResourceType, ResourceRegion: c.ResourceRegion, ResourceOwnerID: c.ResourceOwnerID,
		VpcID: c.VPCID, AvailabilityZone: c.AvailabilityZone, ComplianceStatus: c.ComplianceStatus,
		ManagementState: c.ManagementState, OverlapStatus: c.OverlapStatus, IPUsage: c.IPUsage,
		Tags: toTagItems(c.Tags),
	}
}
