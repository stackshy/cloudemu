package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) ipam() (netdriver.IPAM, bool) {
	i, ok := h.vpc.(netdriver.IPAM)

	return i, ok
}

type ipamXML struct {
	IpamID                string     `xml:"ipamId"`
	IpamArn               string     `xml:"ipamArn"`
	IpamRegion            string     `xml:"ipamRegion,omitempty"`
	PublicDefaultScopeID  string     `xml:"publicDefaultScopeId"`
	PrivateDefaultScopeID string     `xml:"privateDefaultScopeId"`
	ScopeCount            int        `xml:"scopeCount"`
	OperatingRegions      []opRegXML `xml:"operatingRegionSet>item,omitempty"`
	Description           string     `xml:"description,omitempty"`
	Tier                  string     `xml:"tier,omitempty"`
	State                 string     `xml:"state"`
	Tags                  []tagItem  `xml:"tagSet>item,omitempty"`
}

type opRegXML struct {
	RegionName string `xml:"regionName"`
}

type ipamScopeXML struct {
	IpamScopeID   string    `xml:"ipamScopeId"`
	IpamScopeArn  string    `xml:"ipamScopeArn"`
	IpamArn       string    `xml:"ipamArn"`
	IpamScopeType string    `xml:"ipamScopeType"`
	IsDefault     bool      `xml:"isDefault"`
	PoolCount     int       `xml:"poolCount"`
	Description   string    `xml:"description,omitempty"`
	State         string    `xml:"state"`
	Tags          []tagItem `xml:"tagSet>item,omitempty"`
}

type ipamPoolXML struct {
	IpamPoolID                     string    `xml:"ipamPoolId"`
	IpamPoolArn                    string    `xml:"ipamPoolArn"`
	IpamScopeArn                   string    `xml:"ipamScopeArn"`
	IpamScopeType                  string    `xml:"ipamScopeType"`
	AddressFamily                  string    `xml:"addressFamily"`
	Locale                         string    `xml:"locale,omitempty"`
	PoolDepth                      int       `xml:"poolDepth"`
	Description                    string    `xml:"description,omitempty"`
	State                          string    `xml:"state"`
	AllocationMinNetmaskLength     int       `xml:"allocationMinNetmaskLength,omitempty"`
	AllocationMaxNetmaskLength     int       `xml:"allocationMaxNetmaskLength,omitempty"`
	AllocationDefaultNetmaskLength int       `xml:"allocationDefaultNetmaskLength,omitempty"`
	Tags                           []tagItem `xml:"tagSet>item,omitempty"`
}

type ipamPoolCidrXML struct {
	IpamPoolCidrID string `xml:"ipamPoolCidrId"`
	Cidr           string `xml:"cidr,omitempty"`
	NetmaskLength  int    `xml:"netmaskLength,omitempty"`
	State          string `xml:"state"`
}

type ipamAllocationXML struct {
	IpamPoolAllocationID string    `xml:"ipamPoolAllocationId"`
	Cidr                 string    `xml:"cidr,omitempty"`
	ResourceType         string    `xml:"resourceType,omitempty"`
	ResourceID           string    `xml:"resourceId,omitempty"`
	Description          string    `xml:"description,omitempty"`
	Tags                 []tagItem `xml:"tagSet>item,omitempty"`
}

//nolint:gocyclo // flat action dispatch table
func (h *Handler) routeIPAM(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, ok := h.ipam()
	if !ok {
		return false
	}

	switch action {
	case "CreateIpam":
		h.createIpam(w, r, ip)
	case "DescribeIpams":
		h.describeIpams(w, r, ip)
	case "ModifyIpam":
		h.modifyIpam(w, r, ip)
	case "DeleteIpam":
		h.deleteIpam(w, r, ip)
	case "CreateIpamScope":
		h.createIpamScope(w, r, ip)
	case "DescribeIpamScopes":
		h.describeIpamScopes(w, r, ip)
	case "ModifyIpamScope":
		h.modifyIpamScope(w, r, ip)
	case "DeleteIpamScope":
		h.deleteIpamScope(w, r, ip)
	case "CreateIpamPool":
		h.createIpamPool(w, r, ip)
	case "DescribeIpamPools":
		h.describeIpamPools(w, r, ip)
	case "ModifyIpamPool":
		h.modifyIpamPool(w, r, ip)
	case "DeleteIpamPool":
		h.deleteIpamPool(w, r, ip)
	case "ProvisionIpamPoolCidr":
		h.provisionIpamPoolCidr(w, r, ip)
	case "DeprovisionIpamPoolCidr":
		h.deprovisionIpamPoolCidr(w, r, ip)
	case "GetIpamPoolCidrs":
		h.getIpamPoolCidrs(w, r, ip)
	case "AllocateIpamPoolCidr":
		h.allocateIpamPoolCidr(w, r, ip)
	case "ReleaseIpamPoolAllocation":
		h.releaseIpamPoolAllocation(w, r, ip)
	case "GetIpamPoolAllocations", "DescribeIpamPoolAllocations":
		h.getIpamPoolAllocations(w, r, ip)
	case "ModifyIpamPoolAllocation":
		h.modifyIpamPoolAllocation(w, r, ip)
	default:
		return false
	}

	return true
}

func writeIPAMErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidIpamId.NotFound", "IncorrectState")
}

// ---- IPAM ----

func (*Handler) createIpam(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.CreateIpam(r.Context(), netdriver.IpamConfig{
		Description:      r.Form.Get("Description"),
		Tier:             r.Form.Get("Tier"),
		OperatingRegions: awsquery.ListStrings(r.Form, "OperatingRegion"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam"),
	})
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpam(w, "CreateIpamResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpams(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	items, err := ip.DescribeIpams(r.Context(), awsquery.ListStrings(r.Form, "IpamId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name  `xml:"DescribeIpamsResponse"`
		Xmlns   string    `xml:"xmlns,attr"`
		Req     string    `xml:"requestId"`
		Set     []ipamXML `xml:"ipamSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpam(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.ModifyIpam(r.Context(), r.Form.Get("IpamId"), r.Form.Get("Description"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpam(w, "ModifyIpamResponse", out)
}

func (*Handler) deleteIpam(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.DeleteIpam(r.Context(), r.Form.Get("IpamId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpam(w, "DeleteIpamResponse", out)
}

func writeIpam(w http.ResponseWriter, root string, out *netdriver.Ipam) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:""`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Ipam    ipamXML  `xml:"ipam"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Ipam: toIpamXML(out)})
}

func toIpamXML(i *netdriver.Ipam) ipamXML {
	x := ipamXML{
		IpamID: i.ID, IpamArn: i.ARN, IpamRegion: i.Region,
		PublicDefaultScopeID: i.PublicDefaultScopeID, PrivateDefaultScopeID: i.PrivateDefaultScopeID,
		ScopeCount: i.ScopeCount, Description: i.Description, Tier: i.Tier, State: i.State,
		Tags: toTagItems(i.Tags),
	}

	for _, reg := range i.OperatingRegions {
		x.OperatingRegions = append(x.OperatingRegions, opRegXML{RegionName: reg})
	}

	return x
}

// ---- IPAM Scope ----

func (*Handler) createIpamScope(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.CreateIpamScope(r.Context(), netdriver.IpamScopeConfig{
		IpamID:      r.Form.Get("IpamId"),
		Description: r.Form.Get("Description"),
		Tags:        mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-scope"),
	})
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamScope(w, "CreateIpamScopeResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamScopes(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	items, err := ip.DescribeIpamScopes(r.Context(), awsquery.ListStrings(r.Form, "IpamScopeId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamScopeXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamScopeXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name       `xml:"DescribeIpamScopesResponse"`
		Xmlns   string         `xml:"xmlns,attr"`
		Req     string         `xml:"requestId"`
		Set     []ipamScopeXML `xml:"ipamScopeSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamScope(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.ModifyIpamScope(r.Context(), r.Form.Get("IpamScopeId"), r.Form.Get("Description"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamScope(w, "ModifyIpamScopeResponse", out)
}

func (*Handler) deleteIpamScope(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.DeleteIpamScope(r.Context(), r.Form.Get("IpamScopeId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamScope(w, "DeleteIpamScopeResponse", out)
}

func writeIpamScope(w http.ResponseWriter, root string, out *netdriver.IpamScope) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name     `xml:""`
		Xmlns   string       `xml:"xmlns,attr"`
		Req     string       `xml:"requestId"`
		Scope   ipamScopeXML `xml:"ipamScope"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Scope: toIpamScopeXML(out)})
}

func toIpamScopeXML(s *netdriver.IpamScope) ipamScopeXML {
	return ipamScopeXML{
		IpamScopeID: s.ID, IpamScopeArn: s.ARN, IpamArn: s.IpamARN, IpamScopeType: s.ScopeType,
		IsDefault: s.IsDefault, PoolCount: s.PoolCount, Description: s.Description, State: s.State,
		Tags: toTagItems(s.Tags),
	}
}

// ---- IPAM Pool ----

func (*Handler) createIpamPool(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.CreateIpamPool(r.Context(), netdriver.IpamPoolConfig{
		IpamScopeID:                    r.Form.Get("IpamScopeId"),
		AddressFamily:                  r.Form.Get("AddressFamily"),
		Locale:                         r.Form.Get("Locale"),
		Description:                    r.Form.Get("Description"),
		AllocationMinNetmaskLength:     atoiDefault(r.Form.Get("AllocationMinNetmaskLength")),
		AllocationMaxNetmaskLength:     atoiDefault(r.Form.Get("AllocationMaxNetmaskLength")),
		AllocationDefaultNetmaskLength: atoiDefault(r.Form.Get("AllocationDefaultNetmaskLength")),
		Tags:                           mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-pool"),
	})
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPool(w, "CreateIpamPoolResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamPools(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	items, err := ip.DescribeIpamPools(r.Context(), awsquery.ListStrings(r.Form, "IpamPoolId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamPoolXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamPoolXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name      `xml:"DescribeIpamPoolsResponse"`
		Xmlns   string        `xml:"xmlns,attr"`
		Req     string        `xml:"requestId"`
		Set     []ipamPoolXML `xml:"ipamPoolSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamPool(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.ModifyIpamPool(r.Context(), r.Form.Get("IpamPoolId"), r.Form.Get("Description"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPool(w, "ModifyIpamPoolResponse", out)
}

func (*Handler) deleteIpamPool(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.DeleteIpamPool(r.Context(), r.Form.Get("IpamPoolId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPool(w, "DeleteIpamPoolResponse", out)
}

func writeIpamPool(w http.ResponseWriter, root string, out *netdriver.IpamPool) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name    `xml:""`
		Xmlns   string      `xml:"xmlns,attr"`
		Req     string      `xml:"requestId"`
		Pool    ipamPoolXML `xml:"ipamPool"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Pool: toIpamPoolXML(out)})
}

func toIpamPoolXML(p *netdriver.IpamPool) ipamPoolXML {
	return ipamPoolXML{
		IpamPoolID: p.ID, IpamPoolArn: p.ARN, IpamScopeArn: p.IpamScopeARN, IpamScopeType: p.IpamScopeType,
		AddressFamily: p.AddressFamily, Locale: p.Locale, PoolDepth: p.PoolDepth,
		Description: p.Description, State: p.State,
		AllocationMinNetmaskLength: p.AllocationMinNetmaskLength, AllocationMaxNetmaskLength: p.AllocationMaxNetmaskLength,
		AllocationDefaultNetmaskLength: p.AllocationDefaultNetmaskLength, Tags: toTagItems(p.Tags),
	}
}

// ---- Pool CIDRs ----

func (*Handler) provisionIpamPoolCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.ProvisionIpamPoolCidr(r.Context(),
		r.Form.Get("IpamPoolId"), r.Form.Get("Cidr"), atoiDefault(r.Form.Get("NetmaskLength")))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPoolCidr(w, "ProvisionIpamPoolCidrResponse", out)
}

func (*Handler) deprovisionIpamPoolCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.DeprovisionIpamPoolCidr(r.Context(), r.Form.Get("IpamPoolId"), r.Form.Get("Cidr"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPoolCidr(w, "DeprovisionIpamPoolCidrResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) getIpamPoolCidrs(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	items, err := ip.GetIpamPoolCidrs(r.Context(), r.Form.Get("IpamPoolId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamPoolCidrXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamPoolCidrXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name          `xml:"GetIpamPoolCidrsResponse"`
		Xmlns   string            `xml:"xmlns,attr"`
		Req     string            `xml:"requestId"`
		Set     []ipamPoolCidrXML `xml:"ipamPoolCidrSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func writeIpamPoolCidr(w http.ResponseWriter, root string, out *netdriver.IpamPoolCidr) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name        `xml:""`
		Xmlns   string          `xml:"xmlns,attr"`
		Req     string          `xml:"requestId"`
		Cidr    ipamPoolCidrXML `xml:"ipamPoolCidr"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Cidr: toIpamPoolCidrXML(out)})
}

func toIpamPoolCidrXML(c *netdriver.IpamPoolCidr) ipamPoolCidrXML {
	return ipamPoolCidrXML{
		IpamPoolCidrID: c.ID, Cidr: c.CIDR, NetmaskLength: c.NetmaskLength, State: c.State,
	}
}

// ---- Pool Allocations ----

func (*Handler) allocateIpamPoolCidr(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.AllocateIpamPoolCidr(r.Context(), netdriver.AllocateIpamPoolCidrConfig{
		IpamPoolID:    r.Form.Get("IpamPoolId"),
		CIDR:          r.Form.Get("Cidr"),
		NetmaskLength: atoiDefault(r.Form.Get("NetmaskLength")),
		Description:   r.Form.Get("Description"),
	})
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamAllocation(w, "AllocateIpamPoolCidrResponse", out)
}

func (*Handler) releaseIpamPoolAllocation(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	err := ip.ReleaseIpamPoolAllocation(r.Context(), r.Form.Get("IpamPoolId"), r.Form.Get("IpamPoolAllocationId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"ReleaseIpamPoolAllocationResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Success bool     `xml:"success"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Success: true})
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) getIpamPoolAllocations(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	items, err := ip.GetIpamPoolAllocations(r.Context(), r.Form.Get("IpamPoolId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamAllocationXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamAllocationXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name            `xml:"GetIpamPoolAllocationsResponse"`
		Xmlns   string              `xml:"xmlns,attr"`
		Req     string              `xml:"requestId"`
		Set     []ipamAllocationXML `xml:"ipamPoolAllocationSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamPoolAllocation(w http.ResponseWriter, r *http.Request, ip netdriver.IPAM) {
	out, err := ip.ModifyIpamPoolAllocation(r.Context(), r.Form.Get("IpamPoolAllocationId"), r.Form.Get("Description"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamAllocation(w, "ModifyIpamPoolAllocationResponse", out)
}

func writeIpamAllocation(w http.ResponseWriter, root string, out *netdriver.IpamPoolAllocation) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name          `xml:""`
		Xmlns   string            `xml:"xmlns,attr"`
		Req     string            `xml:"requestId"`
		Alloc   ipamAllocationXML `xml:"ipamPoolAllocation"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Alloc: toIpamAllocationXML(out)})
}

func toIpamAllocationXML(a *netdriver.IpamPoolAllocation) ipamAllocationXML {
	return ipamAllocationXML{
		IpamPoolAllocationID: a.ID, Cidr: a.CIDR, ResourceType: a.ResourceType, ResourceID: a.ResourceID,
		Description: a.Description, Tags: toTagItems(a.Tags),
	}
}

func atoiDefault(s string) int {
	n, _ := strconv.Atoi(s)

	return n
}
