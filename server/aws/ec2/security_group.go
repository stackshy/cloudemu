package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

func errMissingGroupID() error {
	return cerrors.New(cerrors.InvalidArgument, "GroupId is required")
}

func errMissingRule() error {
	return cerrors.New(cerrors.InvalidArgument, "at least one IpPermissions rule is required")
}

// newInvalidParameterErr wraps a string in an InvalidArgument cerror so
// writeErrWithNotFound maps it to an "InvalidParameterValue" response.
func newInvalidParameterErr(msg string) error {
	return cerrors.New(cerrors.InvalidArgument, msg)
}

type ipRangeXML struct {
	CidrIP string `xml:"cidrIp"`
}

type ipPermissionXML struct {
	IPProtocol string       `xml:"ipProtocol"`
	FromPort   int          `xml:"fromPort"`
	ToPort     int          `xml:"toPort"`
	IPRanges   []ipRangeXML `xml:"ipRanges>item,omitempty"`
}

type securityGroupXML struct {
	OwnerID             string            `xml:"ownerId"`
	GroupID             string            `xml:"groupId"`
	GroupName           string            `xml:"groupName"`
	GroupDescription    string            `xml:"groupDescription"`
	VpcID               string            `xml:"vpcId,omitempty"`
	IPPermissions       []ipPermissionXML `xml:"ipPermissions>item,omitempty"`
	IPPermissionsEgress []ipPermissionXML `xml:"ipPermissionsEgress>item,omitempty"`
	Tags                []tagItem         `xml:"tagSet>item,omitempty"`
}

type createSecurityGroupResponseXML struct {
	XMLName   xml.Name `xml:"CreateSecurityGroupResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	GroupID   string   `xml:"groupId"`
	Return    bool     `xml:"return"`
}

type describeSecurityGroupsResponseXML struct {
	XMLName          xml.Name           `xml:"DescribeSecurityGroupsResponse"`
	Xmlns            string             `xml:"xmlns,attr"`
	RequestID        string             `xml:"requestId"`
	SecurityGroupSet []securityGroupXML `xml:"securityGroupInfo>item"`
}

func (h *Handler) createSecurityGroup(w http.ResponseWriter, r *http.Request) {
	cfg := netdriver.SecurityGroupConfig{
		Name:        r.Form.Get("GroupName"),
		Description: r.Form.Get("GroupDescription"),
		VPCID:       r.Form.Get("VpcId"),
		Tags:        mergeTagSpecs(awsquery.TagSpecs(r.Form), "security-group"),
	}

	info, err := h.vpc.CreateSecurityGroup(r.Context(), cfg)
	if err != nil {
		writeSGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createSecurityGroupResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		GroupID:   info.ID,
		Return:    true,
	})
}

func (h *Handler) deleteSecurityGroup(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("GroupId")
	if id == "" {
		// Some SDK calls pass GroupName for EC2-Classic SGs. We only support
		// VPC SGs (identified by GroupId); treat missing GroupId as invalid.
		writeSGErr(w, errMissingGroupID())
		return
	}

	if err := h.vpc.DeleteSecurityGroup(r.Context(), id); err != nil {
		writeSGErr(w, err)
		return
	}

	writeSimpleSGResponse(w, "DeleteSecurityGroupResponse")
}

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/igw/route_table
func (h *Handler) describeSecurityGroups(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "GroupId")

	sgs, err := h.vpc.DescribeSecurityGroups(r.Context(), ids)
	if err != nil {
		writeSGErr(w, err)
		return
	}

	out := make([]securityGroupXML, 0, len(sgs))
	for i := range sgs {
		out = append(out, toSecurityGroupXML(&sgs[i]))
	}

	awsquery.WriteXMLResponse(w, describeSecurityGroupsResponseXML{
		Xmlns:            awsquery.Namespace,
		RequestID:        awsquery.RequestID,
		SecurityGroupSet: out,
	})
}

func (h *Handler) authorizeSecurityGroupIngress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, h.vpc.AddIngressRule, "AuthorizeSecurityGroupIngressResponse")
}

func (h *Handler) authorizeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, h.vpc.AddEgressRule, "AuthorizeSecurityGroupEgressResponse")
}

func (h *Handler) revokeSecurityGroupIngress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, h.vpc.RemoveIngressRule, "RevokeSecurityGroupIngressResponse")
}

func (h *Handler) revokeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, h.vpc.RemoveEgressRule, "RevokeSecurityGroupEgressResponse")
}

// ruleFunc is the common signature of AddIngressRule / AddEgressRule /
// RemoveIngressRule / RemoveEgressRule on the Networking driver.
type ruleFunc func(ctx context.Context, groupID string, rule netdriver.SecurityRule) error

// applyRules is the shared Authorize/Revoke path. The driver takes one rule
// at a time; we unroll the IpPermissions.N.IpRanges.M matrix into a flat
// list and apply each entry. The receiver is unused — apply is already bound
// to the caller's driver — kept for API symmetry with other routeVPC methods.
func (*Handler) applyRules(
	w http.ResponseWriter,
	r *http.Request,
	apply ruleFunc,
	responseName string,
) {
	groupID := r.Form.Get("GroupId")
	if groupID == "" {
		writeSGErr(w, errMissingGroupID())
		return
	}

	rules := parseIPPermissions(r.Form)
	if len(rules) == 0 {
		writeSGErr(w, errMissingRule())
		return
	}

	for _, rule := range rules {
		if err := apply(r.Context(), groupID, rule); err != nil {
			writeSGErr(w, err)
			return
		}
	}

	writeSimpleSGResponse(w, responseName)
}

// parseIPPermissions flattens the nested AWS wire form
//
//	IpPermissions.N.IpProtocol=...
//	IpPermissions.N.FromPort=...
//	IpPermissions.N.ToPort=...
//	IpPermissions.N.IpRanges.M.CidrIp=...
//
// into a flat []SecurityRule where each (permission, cidr) pair is one rule.
// This matches how the CloudEmu driver represents rules internally.
func parseIPPermissions(form url.Values) []netdriver.SecurityRule {
	const prefix = "IpPermissions"

	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	var rules []netdriver.SecurityRule

	for _, idx := range indices {
		base := prefix + "." + strconv.Itoa(idx)
		proto := form.Get(base + ".IpProtocol")
		fromPort, _ := strconv.Atoi(form.Get(base + ".FromPort"))
		toPort, _ := strconv.Atoi(form.Get(base + ".ToPort"))
		cidrs := cidrsFromNested(form, base+".IpRanges")

		if len(cidrs) == 0 {
			rules = append(rules, netdriver.SecurityRule{
				Protocol: proto, FromPort: fromPort, ToPort: toPort,
			})

			continue
		}

		for _, cidr := range cidrs {
			rules = append(rules, netdriver.SecurityRule{
				Protocol: proto, FromPort: fromPort, ToPort: toPort, CIDR: cidr,
			})
		}
	}

	return rules
}

// cidrsFromNested reads IpRanges.M.CidrIp values for a given base prefix.
func cidrsFromNested(form url.Values, prefix string) []string {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]string, 0, len(indices))

	for _, idx := range indices {
		key := prefix + "." + strconv.Itoa(idx) + ".CidrIp"
		if v := form.Get(key); v != "" {
			out = append(out, v)
		}
	}

	return out
}

func toSecurityGroupXML(s *netdriver.SecurityGroupInfo) securityGroupXML {
	return securityGroupXML{
		OwnerID:             ownerID,
		GroupID:             s.ID,
		GroupName:           s.Name,
		GroupDescription:    s.Description,
		VpcID:               s.VPCID,
		IPPermissions:       toIPPermissionXMLs(s.IngressRules),
		IPPermissionsEgress: toIPPermissionXMLs(s.EgressRules),
		Tags:                toTagItems(s.Tags),
	}
}

// toIPPermissionXMLs groups rules by (protocol, fromPort, toPort) so each
// entry in the response carries all its CIDR ranges in one <item>. That's
// how real AWS shapes the DescribeSecurityGroups payload.
func toIPPermissionXMLs(rules []netdriver.SecurityRule) []ipPermissionXML {
	if len(rules) == 0 {
		return nil
	}

	type key struct {
		protocol string
		from     int
		to       int
	}

	byKey := make(map[key]*ipPermissionXML)
	order := []key{}

	for _, rule := range rules {
		k := key{protocol: rule.Protocol, from: rule.FromPort, to: rule.ToPort}

		existing, ok := byKey[k]
		if !ok {
			existing = &ipPermissionXML{
				IPProtocol: rule.Protocol,
				FromPort:   rule.FromPort,
				ToPort:     rule.ToPort,
			}
			byKey[k] = existing

			order = append(order, k)
		}

		if rule.CIDR != "" {
			existing.IPRanges = append(existing.IPRanges, ipRangeXML{CidrIP: rule.CIDR})
		}
	}

	out := make([]ipPermissionXML, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}

	return out
}

// writeSimpleSGResponse writes a <return>true</return>-shaped response for
// ops that carry no payload (Delete, Authorize, Revoke). The root element
// name varies per op, which is why we build the envelope manually rather
// than going through WriteXMLResponse.
func writeSimpleSGResponse(w http.ResponseWriter, rootName string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)

	body := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
			`<%s xmlns="%s"><requestId>%s</requestId><return>true</return></%s>`,
		rootName, awsquery.Namespace, awsquery.RequestID, rootName,
	)

	_, _ = w.Write([]byte(body))
}

func writeSGErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidGroup.NotFound", "DependencyViolation")
}
