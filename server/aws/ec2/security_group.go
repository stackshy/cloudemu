package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// tagKeyFilter is the DescribeSecurityGroups filter that matches on the
// presence of a tag key (as opposed to "tag:<key>", which matches a value).
const tagKeyFilter = "tag-key"

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
	XMLName   xml.Name  `xml:"CreateSecurityGroupResponse"`
	Xmlns     string    `xml:"xmlns,attr"`
	RequestID string    `xml:"requestId"`
	GroupID   string    `xml:"groupId"`
	Tags      []tagItem `xml:"tagSet>item,omitempty"`
	Return    bool      `xml:"return"`
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

	// Group names must be unique within a VPC — real EC2 answers
	// InvalidGroup.Duplicate. The driver accepts duplicates, so enforce it
	// here at the wire layer where the AWS-shaped error code lives.
	if dup := h.duplicateGroupName(r.Context(), cfg.Name, cfg.VPCID); dup {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidGroup.Duplicate",
			fmt.Sprintf("The security group '%s' already exists for VPC '%s'", cfg.Name, cfg.VPCID))
		return
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
		Tags:      toTagItems(info.Tags),
		Return:    true,
	})
}

// duplicateGroupName reports whether a security group with the same name
// already exists in the same VPC. A blank name or VPC (EC2-Classic) is never
// treated as a duplicate.
func (h *Handler) duplicateGroupName(ctx context.Context, name, vpcID string) bool {
	if name == "" || vpcID == "" {
		return false
	}

	sgs, err := h.vpc.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return false
	}

	for i := range sgs {
		if sgs[i].VPCID == vpcID && sgs[i].Name == name {
			return true
		}
	}

	return false
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

func (h *Handler) describeSecurityGroups(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "GroupId")

	filters := awsquery.Filters(r.Form)
	if err := validateSGFilters(filters); err != nil {
		writeSGErr(w, err)
		return
	}

	sgs, err := h.vpc.DescribeSecurityGroups(r.Context(), ids)
	if err != nil {
		writeSGErr(w, err)
		return
	}

	out := make([]securityGroupXML, 0, len(sgs))

	for i := range sgs {
		if !sgMatchesFilters(&sgs[i], filters) {
			continue
		}

		out = append(out, toSecurityGroupXML(&sgs[i]))
	}

	awsquery.WriteXMLResponse(w, describeSecurityGroupsResponseXML{
		Xmlns:            awsquery.Namespace,
		RequestID:        awsquery.RequestID,
		SecurityGroupSet: out,
	})
}

// sgScalarFilterField maps a supported scalar filter name to the group field it
// selects on. The second result reports whether the name is a recognized scalar
// filter (tag filters are handled separately in sgMatchesFilters).
func sgScalarFilterField(sg *netdriver.SecurityGroupInfo, name string) (string, bool) {
	switch name {
	case "group-id":
		return sg.ID, true
	case "group-name":
		return sg.Name, true
	case "vpc-id":
		return sg.VPCID, true
	case "description":
		return sg.Description, true
	default:
		return "", false
	}
}

// isSGTagFilter reports whether a filter name targets tags: "tag-key" matches
// on the presence of a key, and "tag:<key>" matches a specific key's value.
func isSGTagFilter(name string) bool {
	return name == tagKeyFilter || strings.HasPrefix(name, "tag:")
}

// validateSGFilters rejects filter names this emulator does not implement, the
// same way real EC2 answers InvalidParameterValue for an unknown filter.
func validateSGFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		if isSGTagFilter(f.Name) {
			continue
		}

		if _, ok := sgScalarFilterField(&netdriver.SecurityGroupInfo{}, f.Name); !ok {
			return newInvalidParameterErr(fmt.Sprintf("The filter '%s' is invalid", f.Name))
		}
	}

	return nil
}

func sgMatchesFilters(sg *netdriver.SecurityGroupInfo, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !sgMatchesFilter(sg, f) {
			return false
		}
	}

	return true
}

func sgMatchesFilter(sg *netdriver.SecurityGroupInfo, f awsquery.Filter) bool {
	if f.Name == tagKeyFilter {
		for key := range sg.Tags {
			if containsString(f.Values, key) {
				return true
			}
		}

		return false
	}

	if key, ok := strings.CutPrefix(f.Name, "tag:"); ok {
		return containsString(f.Values, sg.Tags[key])
	}

	field, ok := sgScalarFilterField(sg, f.Name)
	if !ok {
		return false
	}

	return containsString(f.Values, field)
}

func (h *Handler) authorizeSecurityGroupIngress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, ruleApply{
		apply:        h.vpc.AddIngressRule,
		responseName: "AuthorizeSecurityGroupIngressResponse",
		existing:     func(sg *netdriver.SecurityGroupInfo) []netdriver.SecurityRule { return sg.IngressRules },
	})
}

func (h *Handler) authorizeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, ruleApply{
		apply:        h.vpc.AddEgressRule,
		responseName: "AuthorizeSecurityGroupEgressResponse",
		existing:     func(sg *netdriver.SecurityGroupInfo) []netdriver.SecurityRule { return sg.EgressRules },
	})
}

func (h *Handler) revokeSecurityGroupIngress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, ruleApply{
		apply:        tolerateMissingRule(h.vpc.RemoveIngressRule),
		responseName: "RevokeSecurityGroupIngressResponse",
	})
}

func (h *Handler) revokeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, ruleApply{
		apply:        tolerateMissingRule(h.vpc.RemoveEgressRule),
		responseName: "RevokeSecurityGroupEgressResponse",
	})
}

// tolerateMissingRule makes a Revoke idempotent: real EC2 does not fail when a
// revoked rule is absent, and IaC tools depend on it — Terraform strips the
// default egress rules (IPv4 and IPv6) from a new group, but a v4-only VPC has
// no IPv6 default to remove.
func tolerateMissingRule(remove ruleFunc) ruleFunc {
	return func(ctx context.Context, groupID string, rule netdriver.SecurityRule) error {
		if err := remove(ctx, groupID, rule); err != nil && !cerrors.IsNotFound(err) {
			return err
		}

		return nil
	}
}

// ruleFunc is the common signature of AddIngressRule / AddEgressRule /
// RemoveIngressRule / RemoveEgressRule on the Networking driver.
type ruleFunc func(ctx context.Context, groupID string, rule netdriver.SecurityRule) error

// ruleApply parameterizes the shared Authorize/Revoke path. existing is set only
// for Authorize: it selects the current rule set (ingress or egress) so a
// duplicate can be rejected with InvalidPermission.Duplicate the way real EC2
// does. It is nil for Revoke, which is idempotent.
type ruleApply struct {
	apply        ruleFunc
	responseName string
	existing     func(*netdriver.SecurityGroupInfo) []netdriver.SecurityRule
}

// applyRules is the shared Authorize/Revoke path. The driver takes one rule
// at a time; we unroll the IpPermissions.N.IpRanges.M matrix into a flat
// list and apply each entry.
func (h *Handler) applyRules(w http.ResponseWriter, r *http.Request, spec ruleApply) {
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

	if spec.existing != nil && h.hasDuplicateRule(r.Context(), groupID, rules, spec.existing) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidPermission.Duplicate",
			"the specified rule already exists")
		return
	}

	for _, rule := range rules {
		if err := spec.apply(r.Context(), groupID, rule); err != nil {
			writeSGErr(w, err)
			return
		}
	}

	writeSimpleSGResponse(w, spec.responseName)
}

// hasDuplicateRule reports whether any of the rules being authorized already
// exists in the group's current rule set (as selected by existing).
func (h *Handler) hasDuplicateRule(
	ctx context.Context,
	groupID string,
	rules []netdriver.SecurityRule,
	existing func(*netdriver.SecurityGroupInfo) []netdriver.SecurityRule,
) bool {
	sgs, err := h.vpc.DescribeSecurityGroups(ctx, []string{groupID})
	if err != nil || len(sgs) == 0 {
		return false
	}

	current := existing(&sgs[0])

	for _, rule := range rules {
		for i := range current {
			if current[i] == rule {
				return true
			}
		}
	}

	return false
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
