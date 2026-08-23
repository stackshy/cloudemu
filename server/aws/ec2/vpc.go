package ec2

import (
	"encoding/xml"
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// stateAvailable is the "ready for use" state name shared by VPCs, subnets,
// and most VPC resources in AWS.
const stateAvailable = "available"

// dhcpDefault is the id EC2 reports for a VPC using the Amazon-provided DHCP
// option set (no explicit set associated).
const dhcpDefault = "default"

type vpcXML struct {
	VpcID           string    `xml:"vpcId"`
	State           string    `xml:"state"`
	CidrBlock       string    `xml:"cidrBlock"`
	DhcpOptionsID   string    `xml:"dhcpOptionsId"`
	InstanceTenancy string    `xml:"instanceTenancy"`
	IsDefault       bool      `xml:"isDefault"`
	OwnerID         string    `xml:"ownerId"`
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

//nolint:dupl // per-resource describe+filter pattern; sibling in internet_gateway
func (h *Handler) describeVpcs(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VpcId")

	vpcs, err := h.vpc.DescribeVPCs(r.Context(), ids)
	if err != nil {
		writeVPCErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateVpcFilters(filters); err != nil {
		writeVPCErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, describeVpcsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VpcSet:    filterXML(vpcs, filters, vpcMatchesFilters, toVpcXML),
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
		DhcpOptionsID:   nonEmpty(v.DhcpOptionsID, dhcpDefault),
		InstanceTenancy: "default",
		IsDefault:       false,
		OwnerID:         ownerID,
		Tags:            toTagItems(v.Tags),
	}
}

// validateVpcFilters rejects filter names DescribeVpcs does not model. Silently
// matching nothing would tell a data-source lookup the VPC is absent.
func validateVpcFilters(filters []awsquery.Filter) error {
	var probe netdriver.VPCInfo

	for _, f := range filters {
		if _, known := vpcFilterMatch(&probe, f); !known {
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

func vpcMatchesFilters(v *netdriver.VPCInfo, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if matched, _ := vpcFilterMatch(v, f); !matched {
			return false
		}
	}

	return true
}

// vpcFilterMatch reports whether v satisfies filter f and whether f is a filter
// DescribeVpcs recognizes. Keeping the two answers in one function means each
// filter name is written exactly once.
func vpcFilterMatch(v *netdriver.VPCInfo, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case filterVPCID:
		return containsString(f.Values, v.ID), true
	case filterCIDR, filterCIDRBlock, "cidr-block-association.cidr-block":
		return containsString(f.Values, v.CIDRBlock), true
	case filterState:
		return containsString(f.Values, nonEmpty(v.State, stateAvailable)), true
	case "dhcp-options-id":
		return containsString(f.Values, nonEmpty(v.DhcpOptionsID, dhcpDefault)), true
	case "isDefault", "is-default":
		return containsString(f.Values, boolFilterValue(false)), true
	default:
		return tagFilterMatch(f.Name, f.Values, v.Tags)
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
