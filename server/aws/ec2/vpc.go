package ec2

import (
	"encoding/xml"
	"net/http"
	"sort"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// stateAvailable is the "ready for use" state name shared by VPCs, subnets,
// and most VPC resources in AWS.
const stateAvailable = "available"

type vpcXML struct {
	VpcID           string    `xml:"vpcId"`
	State           string    `xml:"state"`
	CidrBlock       string    `xml:"cidrBlock"`
	DhcpOptionsID   string    `xml:"dhcpOptionsId"`
	InstanceTenancy string    `xml:"instanceTenancy"`
	IsDefault       bool      `xml:"isDefault"`
	Tags            []tagItem `xml:"tagSet>item,omitempty"`
}

type createVpcResponseXML struct {
	XMLName   xml.Name `xml:"CreateVpcResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Vpc       vpcXML   `xml:"vpc"`
}

type describeVpcsResponseXML struct {
	XMLName   xml.Name `xml:"DescribeVpcsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	VpcSet    []vpcXML `xml:"vpcSet>item"`
}

type deleteVpcResponseXML struct {
	XMLName   xml.Name `xml:"DeleteVpcResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createVpc(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.VPCConfig{
		CIDRBlock: r.Form.Get("CidrBlock"),
		Tags:      mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc"),
	}

	info, err := h.vpc.CreateVPC(r.Context(), cfg)
	if err != nil {
		writeVPCErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createVpcResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Vpc:       toVpcXML(info),
	})
}

func (h *Handler) deleteVpc(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteVPC(r.Context(), r.Form.Get("VpcId")); err != nil {
		writeVPCErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteVpcResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe pattern; siblings in subnet/sg/igw/route_table
func (h *Handler) describeVpcs(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VpcId")

	vpcs, err := h.vpc.DescribeVPCs(r.Context(), ids)
	if err != nil {
		writeVPCErr(w, err)
		return
	}

	out := make([]vpcXML, 0, len(vpcs))
	for i := range vpcs {
		out = append(out, toVpcXML(&vpcs[i]))
	}

	awsquery.WriteXMLResponse(w, describeVpcsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VpcSet:    out,
	})
}

func toVpcXML(v *netdriver.VPCInfo) vpcXML {
	state := v.State
	if state == "" {
		state = stateAvailable
	}

	return vpcXML{
		VpcID:           v.ID,
		State:           state,
		CidrBlock:       v.CIDRBlock,
		DhcpOptionsID:   "default",
		InstanceTenancy: "default",
		IsDefault:       false,
		Tags:            toTagItems(v.Tags),
	}
}

// toTagItems converts a tags map into XML items with deterministic ordering.
// Map iteration order isn't stable in Go; sorting keeps responses reproducible.
func toTagItems(tags map[string]string) []tagItem {
	if len(tags) == 0 {
		return nil
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]tagItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, tagItem{Key: k, Value: tags[k]})
	}

	return out
}

// writeVPCErr returns the VPC-specific NotFound code.
func writeVPCErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpcID.NotFound", "DependencyViolation")
}
