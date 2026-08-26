package ec2

import (
	"encoding/xml"
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// stateAvailable is the "ready for use" state name shared by VPCs, subnets,
// and most VPC resources in AWS.
const stateAvailable = "available"

// dhcpDefault is the id EC2 reports for a VPC using the Amazon-provided DHCP
// option set (no explicit set associated).
const dhcpDefault = "default"

// cidrAssocIDPrefix is the id prefix AWS assigns to a VPC's primary-CIDR
// association in cidrBlockAssociationSet.
const cidrAssocIDPrefix = "vpc-cidr-assoc-"

// tenancyDefaultXML is the instanceTenancy value EC2 reports for a VPC created
// without an explicit tenancy (the provider stores this too, so this only
// covers records created outside the AWS wire layer).
const tenancyDefaultXML = "default"

// cidrStateAssociated is the terminal state AWS reports for an in-use VPC CIDR
// association.
const cidrStateAssociated = "associated"

type vpcCidrBlockStateXML struct {
	State string `xml:"state"`
}

// vpcCidrAssocXML is one entry in a VPC's cidrBlockAssociationSet. AWS returns
// the primary CIDR both flat (cidrBlock) and here; IaC tools read the
// association id and state from this set.
type vpcCidrAssocXML struct {
	AssociationID  string               `xml:"associationId"`
	CidrBlock      string               `xml:"cidrBlock"`
	CidrBlockState vpcCidrBlockStateXML `xml:"cidrBlockState"`
}

type vpcXML struct {
	VpcID                   string            `xml:"vpcId"`
	State                   string            `xml:"state"`
	CidrBlock               string            `xml:"cidrBlock"`
	DhcpOptionsID           string            `xml:"dhcpOptionsId"`
	InstanceTenancy         string            `xml:"instanceTenancy"`
	IsDefault               bool              `xml:"isDefault"`
	OwnerID                 string            `xml:"ownerId"`
	CidrBlockAssociationSet []vpcCidrAssocXML `xml:"cidrBlockAssociationSet>item,omitempty"`
	Tags                    []tagItem         `xml:"tagSet>item,omitempty"`
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
	NextToken string   `xml:"nextToken,omitempty"`
}

type deleteVpcResponseXML struct {
	XMLName   xml.Name `xml:"DeleteVpcResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createVpc(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.VPCConfig{
		CIDRBlock:       r.Form.Get("CidrBlock"),
		InstanceTenancy: r.Form.Get("InstanceTenancy"),
		Tags:            mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc"),
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

	page, next := pageNetworkingXML(
		filterXML(vpcs, filters, vpcMatchesFilters, toVpcXML), r,
		func(v vpcXML) string { return v.VpcID })

	awsquery.WriteXMLResponse(w, describeVpcsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VpcSet:    page,
		NextToken: next,
	})
}

func toVpcXML(v *netdriver.VPCInfo) vpcXML {
	state := v.State
	if state == "" {
		state = stateAvailable
	}

	return vpcXML{
		VpcID:                   v.ID,
		State:                   state,
		CidrBlock:               v.CIDRBlock,
		DhcpOptionsID:           nonEmpty(v.DhcpOptionsID, dhcpDefault),
		InstanceTenancy:         nonEmpty(v.InstanceTenancy, tenancyDefaultXML),
		IsDefault:               false,
		OwnerID:                 ownerID,
		CidrBlockAssociationSet: vpcCidrAssociationSet(v),
		Tags:                    toTagItems(v.Tags),
	}
}

// vpcCidrAssociationSet synthesizes the single primary-CIDR association AWS
// returns for a VPC. The association id is derived deterministically from the
// VPC id so it is stable across Describe calls (real AWS ids are stable too).
// IPv6 associations are not modeled, so ipv6CidrBlockAssociationSet stays empty.
func vpcCidrAssociationSet(v *netdriver.VPCInfo) []vpcCidrAssocXML {
	if v.CIDRBlock == "" {
		return nil
	}

	return []vpcCidrAssocXML{{
		AssociationID:  cidrAssocIDPrefix + strings.TrimPrefix(v.ID, "vpc-"),
		CidrBlock:      v.CIDRBlock,
		CidrBlockState: vpcCidrBlockStateXML{State: cidrStateAssociated},
	}}
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
	case filterDHCPOptionsID:
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
	// A VPC CIDR whose netmask is outside /16../28 is reported with the
	// resource-specific InvalidVpcRange code rather than the generic
	// InvalidParameterValue. The provider marks it with the "InvalidVpcRange:"
	// prefix inside the message.
	if cerrors.IsInvalidArgument(err) && strings.Contains(err.Error(), "InvalidVpcRange:") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidVpcRange",
			"The block range must be between a /28 netmask and /16 netmask")
		return
	}

	writeErrWithNotFound(w, err, "InvalidVpcID.NotFound", "DependencyViolation")
}
