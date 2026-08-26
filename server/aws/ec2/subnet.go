package ec2

import (
	"encoding/xml"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type subnetXML struct {
	SubnetID            string    `xml:"subnetId"`
	State               string    `xml:"state"`
	VpcID               string    `xml:"vpcId"`
	CidrBlock           string    `xml:"cidrBlock"`
	AvailableIPCount    int       `xml:"availableIpAddressCount"`
	AvailabilityZone    string    `xml:"availabilityZone"`
	AvailabilityZoneID  string    `xml:"availabilityZoneId,omitempty"`
	DefaultForAz        bool      `xml:"defaultForAz"`
	MapPublicIPOnLaunch bool      `xml:"mapPublicIpOnLaunch"`
	SubnetArn           string    `xml:"subnetArn,omitempty"`
	OwnerID             string    `xml:"ownerId"`
	Tags                []tagItem `xml:"tagSet>item,omitempty"`
}

type createSubnetResponseXML struct {
	XMLName   xml.Name  `xml:"CreateSubnetResponse"`
	Xmlns     string    `xml:"xmlns,attr"`
	RequestID string    `xml:"requestId"`
	Subnet    subnetXML `xml:"subnet"`
}

type describeSubnetsResponseXML struct {
	XMLName   xml.Name    `xml:"DescribeSubnetsResponse"`
	Xmlns     string      `xml:"xmlns,attr"`
	RequestID string      `xml:"requestId"`
	SubnetSet []subnetXML `xml:"subnetSet>item"`
	NextToken string      `xml:"nextToken,omitempty"`
}

type deleteSubnetResponseXML struct {
	XMLName   xml.Name `xml:"DeleteSubnetResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type modifySubnetAttributeResponseXML struct {
	XMLName   xml.Name `xml:"ModifySubnetAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createSubnet(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.SubnetConfig{
		VPCID:            r.Form.Get("VpcId"),
		CIDRBlock:        r.Form.Get("CidrBlock"),
		AvailabilityZone: r.Form.Get("AvailabilityZone"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "subnet"),
	}

	info, err := h.vpc.CreateSubnet(r.Context(), cfg)
	if err != nil {
		writeCreateSubnetErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createSubnetResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Subnet:    toSubnetXML(info, regionFromRequest(r)),
	})
}

func (h *Handler) deleteSubnet(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteSubnet(r.Context(), r.Form.Get("SubnetId")); err != nil {
		writeSubnetErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteSubnetResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) describeSubnets(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "SubnetId")

	subnets, err := h.vpc.DescribeSubnets(r.Context(), ids)
	if err != nil {
		writeSubnetErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateSubnetFilters(filters); err != nil {
		writeSubnetErr(w, err)
		return
	}

	region := regionFromRequest(r)
	toXML := func(s *netdriver.SubnetInfo) subnetXML { return toSubnetXML(s, region) }

	page, next := pageNetworkingXML(
		filterXML(subnets, filters, subnetMatchesFilters, toXML), r,
		func(s subnetXML) string { return s.SubnetID })

	awsquery.WriteXMLResponse(w, describeSubnetsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		SubnetSet: page,
		NextToken: next,
	})
}

// modifySubnetAttribute flips a subnet launch attribute. MapPublicIpOnLaunch is
// the only way to make a subnet hand out public IPv4 addresses, so IaC tools
// building a public subnet depend on it.
func (h *Handler) modifySubnetAttribute(w http.ResponseWriter, r *http.Request) {
	attrs, ok := h.vpc.(netdriver.SubnetAttributes)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"this driver does not model subnet attributes")

		return
	}

	err := attrs.ModifySubnetAttribute(r.Context(), r.Form.Get("SubnetId"),
		netdriver.SubnetAttributeUpdate{
			MapPublicIPOnLaunch: boolAttributeValue(r, "MapPublicIpOnLaunch"),
		})
	if err != nil {
		writeSubnetErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifySubnetAttributeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func toSubnetXML(s *netdriver.SubnetInfo, region string) subnetXML {
	state := s.State
	if state == "" {
		state = stateAvailable
	}

	x := subnetXML{
		SubnetID:            s.ID,
		State:               state,
		VpcID:               s.VPCID,
		CidrBlock:           s.CIDRBlock,
		AvailableIPCount:    s.AvailableIPAddressCount,
		AvailabilityZone:    s.AvailabilityZone,
		AvailabilityZoneID:  zoneIDFor(s.AvailabilityZone),
		MapPublicIPOnLaunch: s.MapPublicIPOnLaunch,
		OwnerID:             ownerID,
		Tags:                toTagItems(s.Tags),
	}

	if s.ID != "" {
		x.SubnetArn = idgen.AWSARN("ec2", region, ownerID, "subnet/"+s.ID)
	}

	return x
}

// zoneIDFor maps an availability-zone name to its zone id (us-east-1a ->
// us-east-1-az1), matching DescribeAvailabilityZones. Returns "" for an unset
// or unrecognized zone rather than inventing an id.
func zoneIDFor(zone string) string {
	if zone == "" {
		return ""
	}

	last := zone[len(zone)-1]
	if last < 'a' || last > 'z' {
		return ""
	}

	return zone[:len(zone)-1] + "-az" + string(rune('1'+(last-'a')))
}

// validateSubnetFilters rejects filter names DescribeSubnets does not model, so
// a data-source lookup is never silently told a subnet is absent.
func validateSubnetFilters(filters []awsquery.Filter) error {
	var probe netdriver.SubnetInfo

	for _, f := range filters {
		if _, known := subnetFilterMatch(&probe, f); !known {
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

func subnetMatchesFilters(s *netdriver.SubnetInfo, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if matched, _ := subnetFilterMatch(s, f); !matched {
			return false
		}
	}

	return true
}

// subnetFilterMatch reports whether s satisfies filter f and whether f is a
// filter DescribeSubnets recognizes.
func subnetFilterMatch(s *netdriver.SubnetInfo, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case filterSubnetID:
		return containsString(f.Values, s.ID), true
	case filterVPCID:
		return containsString(f.Values, s.VPCID), true
	case filterCIDR, filterCIDRBlock, "cidrBlock":
		return containsString(f.Values, s.CIDRBlock), true
	case "availability-zone", "availabilityZone":
		return containsString(f.Values, s.AvailabilityZone), true
	case "availability-zone-id":
		return containsString(f.Values, zoneIDFor(s.AvailabilityZone)), true
	case filterState:
		return containsString(f.Values, nonEmpty(s.State, stateAvailable)), true
	case "default-for-az", "defaultForAz":
		return containsString(f.Values, boolFilterValue(false)), true
	case "map-public-ip-on-launch":
		return containsString(f.Values, boolFilterValue(s.MapPublicIPOnLaunch)), true
	default:
		return tagFilterMatch(f.Name, f.Values, s.Tags)
	}
}

func writeSubnetErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidSubnetID.NotFound", "DependencyViolation")
}

// writeCreateSubnetErr adds the CreateSubnet-only codes: InvalidSubnet.Conflict
// for an overlapping CIDR (surfaced as AlreadyExists by the driver) and
// InvalidSubnet.Range for a CIDR outside the VPC block, falling back to the
// shared subnet error mapping otherwise.
func writeCreateSubnetErr(w http.ResponseWriter, err error) {
	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidSubnet.Conflict", err.Error())
		return
	}

	// A subnet CIDR outside the VPC's CIDR block is InvalidSubnet.Range, not the
	// generic InvalidParameterValue the shared mapper would emit.
	if cerrors.IsInvalidArgument(err) && strings.Contains(err.Error(), "InvalidSubnet.Range:") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidSubnet.Range", cerrors.Message(err))
		return
	}

	writeSubnetErr(w, err)
}
