package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// defaultVPCEndpointType is what real EC2 assumes when CreateVpcEndpoint omits
// VpcEndpointType.
const defaultVPCEndpointType = "Gateway"

type vpcEndpointXML struct {
	VpcEndpointID       string    `xml:"vpcEndpointId"`
	VpcEndpointType     string    `xml:"vpcEndpointType"`
	VpcID               string    `xml:"vpcId"`
	ServiceName         string    `xml:"serviceName"`
	State               string    `xml:"state"`
	RouteTableIDs       []string  `xml:"routeTableIdSet>item,omitempty"`
	SubnetIDs           []string  `xml:"subnetIdSet>item,omitempty"`
	Groups              []string  `xml:"groupSet>item,omitempty"`
	NetworkInterfaceIDs []string  `xml:"networkInterfaceIdSet>item,omitempty"`
	CreationTime        string    `xml:"creationTimestamp,omitempty"`
	Tags                []tagItem `xml:"tagSet>item,omitempty"`
}

func (h *Handler) routeVPCEndpoints(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateVpcEndpoint":
		h.createVPCEndpoint(w, r)
	case "DeleteVpcEndpoints":
		h.deleteVPCEndpoints(w, r)
	case "DescribeVpcEndpoints":
		h.describeVPCEndpoints(w, r)
	case "ModifyVpcEndpoint":
		h.modifyVPCEndpoint(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createVPCEndpoint(w http.ResponseWriter, r *http.Request) {
	endpointType := r.Form.Get("VpcEndpointType")
	if endpointType == "" {
		endpointType = defaultVPCEndpointType
	}

	ep, err := h.vpc.CreateVPCEndpoint(r.Context(), netdriver.VPCEndpointConfig{
		VPCID:            r.Form.Get("VpcId"),
		ServiceName:      r.Form.Get("ServiceName"),
		EndpointType:     endpointType,
		SubnetIDs:        awsquery.ListStrings(r.Form, "SubnetId"),
		SecurityGroupIDs: awsquery.ListStrings(r.Form, "SecurityGroupId"),
		RouteTableIDs:    awsquery.ListStrings(r.Form, "RouteTableId"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc-endpoint"),
	})
	if err != nil {
		writeVPCEndpointErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName  xml.Name       `xml:"CreateVpcEndpointResponse"`
		Xmlns    string         `xml:"xmlns,attr"`
		Req      string         `xml:"requestId"`
		Endpoint vpcEndpointXML `xml:"vpcEndpoint"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Endpoint: toVPCEndpointXML(ep)})
}

// deleteVPCEndpoints is idempotent: like real EC2 it always returns HTTP 200
// and reports ids it could not delete (including unknown vpce-... ids) as
// entries in the <unsuccessful> set rather than a top-level error.
func (h *Handler) deleteVPCEndpoints(w http.ResponseWriter, r *http.Request) {
	var unsuccessful []unsuccessfulItemXML

	for _, id := range awsquery.ListStrings(r.Form, "VpcEndpointId") {
		if err := h.vpc.DeleteVPCEndpoint(r.Context(), id); err != nil {
			item := unsuccessfulItemXML{ResourceID: id}
			item.Error.Code = "InvalidVpcEndpointId.NotFound"
			item.Error.Message = err.Error()
			unsuccessful = append(unsuccessful, item)
		}
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name              `xml:"DeleteVpcEndpointsResponse"`
		Xmlns   string                `xml:"xmlns,attr"`
		Req     string                `xml:"requestId"`
		Unsucc  []unsuccessfulItemXML `xml:"unsuccessful>item,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Unsucc: unsuccessful})
}

func (h *Handler) describeVPCEndpoints(w http.ResponseWriter, r *http.Request) {
	items, err := h.vpc.DescribeVPCEndpoints(r.Context(), awsquery.ListStrings(r.Form, "VpcEndpointId"))
	if err != nil {
		writeVPCEndpointErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)

	out := make([]vpcEndpointXML, 0, len(items))

	for i := range items {
		if vpcEndpointMatchesFilters(&items[i], filters) {
			out = append(out, toVPCEndpointXML(&items[i]))
		}
	}

	page, next := pageNetworkingXML(out, r, func(e vpcEndpointXML) string { return e.VpcEndpointID })

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"DescribeVpcEndpointsResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		Set     []vpcEndpointXML `xml:"vpcEndpointSet>item"`
		Next    string           `xml:"nextToken,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: page, Next: next})
}

// modifyVPCEndpoint applies the Add*/Remove* set mutations real EC2 uses to
// change an endpoint's subnets, route tables, and security groups.
func (h *Handler) modifyVPCEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("VpcEndpointId")

	current, err := h.vpc.DescribeVPCEndpoints(r.Context(), []string{id})
	if err != nil {
		writeVPCEndpointErr(w, err)
		return
	}

	cur := current[0]
	cfg := netdriver.VPCEndpointConfig{
		SubnetIDs:        applySetMutation(cur.SubnetIDs, r, "AddSubnetId", "RemoveSubnetId"),
		RouteTableIDs:    applySetMutation(cur.RouteTableIDs, r, "AddRouteTableId", "RemoveRouteTableId"),
		SecurityGroupIDs: applySetMutation(cur.SecurityGroupIDs, r, "AddSecurityGroupId", "RemoveSecurityGroupId"),
	}

	if _, err := h.vpc.ModifyVPCEndpoint(r.Context(), id, cfg); err != nil {
		writeVPCEndpointErr(w, err)
		return
	}

	writeReturnTrue(w, "ModifyVpcEndpointResponse")
}

// applySetMutation returns cur with the Add* ids appended and the Remove* ids
// dropped, matching how ModifyVpcEndpoint edits an endpoint's id sets.
func applySetMutation(cur []string, r *http.Request, addParam, removeParam string) []string {
	remove := map[string]bool{}
	for _, id := range awsquery.ListStrings(r.Form, removeParam) {
		remove[id] = true
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(cur))

	for _, id := range cur {
		if remove[id] || seen[id] {
			continue
		}

		seen[id] = true

		out = append(out, id)
	}

	for _, id := range awsquery.ListStrings(r.Form, addParam) {
		if remove[id] || seen[id] {
			continue
		}

		seen[id] = true

		out = append(out, id)
	}

	return out
}

func vpcEndpointMatchesFilters(ep *netdriver.VPCEndpoint, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !vpcEndpointMatchesFilter(ep, f) {
			return false
		}
	}

	return true
}

func vpcEndpointMatchesFilter(ep *netdriver.VPCEndpoint, f awsquery.Filter) bool {
	if matched, isTag := matchStorageTagFilter(ep.Tags, f); isTag {
		return matched
	}

	switch f.Name {
	case filterVPCID:
		return containsString(f.Values, ep.VPCID)
	case "service-name":
		return containsString(f.Values, ep.ServiceName)
	case "vpc-endpoint-id":
		return containsString(f.Values, ep.ID)
	case "vpc-endpoint-type":
		return containsString(f.Values, ep.EndpointType)
	case "vpc-endpoint-state":
		return containsString(f.Values, ep.State)
	default:
		return false
	}
}

func toVPCEndpointXML(ep *netdriver.VPCEndpoint) vpcEndpointXML {
	return vpcEndpointXML{
		VpcEndpointID:       ep.ID,
		VpcEndpointType:     nonEmpty(ep.EndpointType, defaultVPCEndpointType),
		VpcID:               ep.VPCID,
		ServiceName:         ep.ServiceName,
		State:               nonEmpty(ep.State, stateAvailable),
		RouteTableIDs:       ep.RouteTableIDs,
		SubnetIDs:           ep.SubnetIDs,
		Groups:              ep.SecurityGroupIDs,
		NetworkInterfaceIDs: ep.NetworkInterfaceIDs,
		CreationTime:        ep.CreatedAt,
		Tags:                toTagItems(ep.Tags),
	}
}

func writeVPCEndpointErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpcEndpointId.NotFound", "DependencyViolation")
}
