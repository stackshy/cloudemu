package ec2

import (
	"encoding/xml"
	"net/http"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// subnetAvailableIPs is the count reported to SDK clients. Real AWS varies
// it by CIDR size; our emulator doesn't track allocation, so a constant fits.
const subnetAvailableIPs = 251

type subnetXML struct {
	SubnetID            string    `xml:"subnetId"`
	State               string    `xml:"state"`
	VpcID               string    `xml:"vpcId"`
	CidrBlock           string    `xml:"cidrBlock"`
	AvailableIPCount    int       `xml:"availableIpAddressCount"`
	AvailabilityZone    string    `xml:"availabilityZone"`
	DefaultForAz        bool      `xml:"defaultForAz"`
	MapPublicIPOnLaunch bool      `xml:"mapPublicIpOnLaunch"`
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
}

type deleteSubnetResponseXML struct {
	XMLName   xml.Name `xml:"DeleteSubnetResponse"`
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
		writeSubnetErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createSubnetResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Subnet:    toSubnetXML(info),
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

//nolint:dupl // per-resource describe pattern; siblings in vpc/sg/igw/route_table
func (h *Handler) describeSubnets(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "SubnetId")

	subnets, err := h.vpc.DescribeSubnets(r.Context(), ids)
	if err != nil {
		writeSubnetErr(w, err)
		return
	}

	out := make([]subnetXML, 0, len(subnets))
	for i := range subnets {
		out = append(out, toSubnetXML(&subnets[i]))
	}

	awsquery.WriteXMLResponse(w, describeSubnetsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		SubnetSet: out,
	})
}

func toSubnetXML(s *netdriver.SubnetInfo) subnetXML {
	state := s.State
	if state == "" {
		state = stateAvailable
	}

	return subnetXML{
		SubnetID:         s.ID,
		State:            state,
		VpcID:            s.VPCID,
		CidrBlock:        s.CIDRBlock,
		AvailableIPCount: subnetAvailableIPs,
		AvailabilityZone: s.AvailabilityZone,
		Tags:             toTagItems(s.Tags),
	}
}

func writeSubnetErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidSubnetID.NotFound", "DependencyViolation")
}
