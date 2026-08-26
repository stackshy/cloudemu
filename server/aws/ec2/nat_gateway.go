package ec2

import (
	"encoding/xml"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type natGatewayAddressXML struct {
	AllocationID       string `xml:"allocationId,omitempty"`
	NetworkInterfaceID string `xml:"networkInterfaceId,omitempty"`
	PrivateIP          string `xml:"privateIp,omitempty"`
	PublicIP           string `xml:"publicIp,omitempty"`
}

type natGatewayXML struct {
	NatGatewayID     string                 `xml:"natGatewayId"`
	VpcID            string                 `xml:"vpcId"`
	SubnetID         string                 `xml:"subnetId"`
	State            string                 `xml:"state"`
	ConnectivityType string                 `xml:"connectivityType,omitempty"`
	CreateTime       string                 `xml:"createTime,omitempty"`
	Addresses        []natGatewayAddressXML `xml:"natGatewayAddressSet>item,omitempty"`
	Tags             []tagItem              `xml:"tagSet>item,omitempty"`
}

type createNatGatewayResponseXML struct {
	XMLName    xml.Name      `xml:"CreateNatGatewayResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	NatGateway natGatewayXML `xml:"natGateway"`
}

type describeNatGatewaysResponseXML struct {
	XMLName       xml.Name        `xml:"DescribeNatGatewaysResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	NatGatewaySet []natGatewayXML `xml:"natGatewaySet>item"`
	NextToken     string          `xml:"nextToken,omitempty"`
}

type deleteNatGatewayResponseXML struct {
	XMLName      xml.Name `xml:"DeleteNatGatewayResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	NatGatewayID string   `xml:"natGatewayId"`
}

func (h *Handler) createNatGateway(w http.ResponseWriter, r *http.Request) {
	info, err := h.vpc.CreateNATGateway(r.Context(), netdriver.NATGatewayConfig{
		SubnetID:         r.Form.Get("SubnetId"),
		AllocationID:     r.Form.Get("AllocationId"),
		ConnectivityType: r.Form.Get("ConnectivityType"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "natgateway"),
	})
	if err != nil {
		writeCreateNatErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createNatGatewayResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		NatGateway: toNatGatewayXML(info),
	})
}

func (h *Handler) deleteNatGateway(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("NatGatewayId")

	if err := h.vpc.DeleteNATGateway(r.Context(), id); err != nil {
		writeNatErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteNatGatewayResponseXML{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		NatGatewayID: id,
	})
}

//nolint:dupl // per-resource describe+filter pattern, mirrors describeNetworkACLs
func (h *Handler) describeNatGateways(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "NatGatewayId")

	nats, err := h.vpc.DescribeNATGateways(r.Context(), ids)
	if err != nil {
		writeNatErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateNetworkingFilters(filters, natFilterMatch); err != nil {
		writeNatErr(w, err)
		return
	}

	out := filterXML(nats, filters, natMatchesFilters, toNatGatewayXML)
	page, next := pageNetworkingXML(out, r, func(n natGatewayXML) string { return n.NatGatewayID })

	awsquery.WriteXMLResponse(w, describeNatGatewaysResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		NatGatewaySet: page,
		NextToken:     next,
	})
}

func natMatchesFilters(n *netdriver.NATGateway, filters []awsquery.Filter) bool {
	return matchNetworkingFilters(n, filters, natFilterMatch)
}

// natFilterMatch reports whether n satisfies filter f and whether f is a filter
// DescribeNatGateways recognizes. State falls back to "available", matching how
// toNatGatewayXML renders an unset state, so a state=available filter finds a
// freshly created gateway.
func natFilterMatch(n *netdriver.NATGateway, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case "nat-gateway-id":
		return containsString(f.Values, n.ID), true
	case filterVPCID:
		return containsString(f.Values, n.VPCID), true
	case filterSubnetID:
		return containsString(f.Values, n.SubnetID), true
	case filterState:
		return containsString(f.Values, nonEmpty(n.State, stateAvailable)), true
	default:
		return tagFilterMatch(f.Name, f.Values, n.Tags)
	}
}

func toNatGatewayXML(n *netdriver.NATGateway) natGatewayXML {
	state := n.State
	if state == "" {
		state = stateAvailable
	}

	return natGatewayXML{
		NatGatewayID:     n.ID,
		VpcID:            n.VPCID,
		SubnetID:         n.SubnetID,
		State:            state,
		ConnectivityType: n.ConnectivityType,
		CreateTime:       n.CreatedAt,
		Addresses: []natGatewayAddressXML{{
			AllocationID:       n.AllocationID,
			NetworkInterfaceID: n.NetworkInterfaceID,
			PrivateIP:          n.PrivateIP,
			PublicIP:           n.PublicIP,
		}},
		Tags: toTagItems(n.Tags),
	}
}

func writeNatErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "NatGatewayNotFound", "DependencyViolation")
}

// writeCreateNatErr maps CreateNatGateway's create-only codes. A not-found on
// create is the target subnet — InvalidSubnetID.NotFound, not the NatGatewayNotFound
// the generic mapper would emit. The Elastic IP pairing errors carry a marker so a
// missing or mismatched AllocationId surfaces the resource-specific EC2 code
// (MissingParameter / InvalidAllocationID.NotFound) rather than a bare
// InvalidParameterValue.
func writeCreateNatErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err) && strings.Contains(err.Error(), "subnet"):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidSubnetID.NotFound", cerrors.Message(err))
	case strings.Contains(err.Error(), "InvalidAllocationID.NotFound:"):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAllocationID.NotFound", cerrors.Message(err))
	case strings.Contains(err.Error(), "MissingParameter:"):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "MissingParameter", cerrors.Message(err))
	default:
		writeNatErr(w, err)
	}
}
