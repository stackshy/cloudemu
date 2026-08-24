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
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// tagKeyFilter is the DescribeSecurityGroups filter that matches on the
// presence of a tag key (as opposed to "tag:<key>", which matches a value).
const tagKeyFilter = "tag-key"

// groupIDFilter and securityGroupRuleIDFilter are the filter names shared by
// DescribeSecurityGroups and DescribeSecurityGroupRules.
const (
	groupIDFilter             = "group-id"
	securityGroupRuleIDFilter = "security-group-rule-id"
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
	CidrIP      string `xml:"cidrIp"`
	Description string `xml:"description,omitempty"`
}

type ipv6RangeXML struct {
	CidrIPv6    string `xml:"cidrIpv6"`
	Description string `xml:"description,omitempty"`
}

type prefixListIDXML struct {
	PrefixListID string `xml:"prefixListId"`
	Description  string `xml:"description,omitempty"`
}

type userIDGroupPairXML struct {
	GroupID     string `xml:"groupId"`
	UserID      string `xml:"userId,omitempty"`
	Description string `xml:"description,omitempty"`
}

type ipPermissionXML struct {
	IPProtocol    string               `xml:"ipProtocol"`
	FromPort      int                  `xml:"fromPort"`
	ToPort        int                  `xml:"toPort"`
	IPRanges      []ipRangeXML         `xml:"ipRanges>item,omitempty"`
	IPv6Ranges    []ipv6RangeXML       `xml:"ipv6Ranges>item,omitempty"`
	PrefixListIDs []prefixListIDXML    `xml:"prefixListIds>item,omitempty"`
	Groups        []userIDGroupPairXML `xml:"groups>item,omitempty"`
}

// referencedGroupInfoXML is the ReferencedSecurityGroup shape nested inside a
// SecurityGroupRule when the rule targets another security group.
type referencedGroupInfoXML struct {
	GroupID string `xml:"groupId,omitempty"`
	UserID  string `xml:"userId,omitempty"`
}

// securityGroupRuleXML is the flat SecurityGroupRule shape returned by
// Authorize*/DescribeSecurityGroupRules (one item per resolved target).
type securityGroupRuleXML struct {
	SecurityGroupRuleID string                  `xml:"securityGroupRuleId"`
	GroupID             string                  `xml:"groupId"`
	GroupOwnerID        string                  `xml:"groupOwnerId"`
	IsEgress            bool                    `xml:"isEgress"`
	IPProtocol          string                  `xml:"ipProtocol"`
	FromPort            int                     `xml:"fromPort"`
	ToPort              int                     `xml:"toPort"`
	CidrIPv4            string                  `xml:"cidrIpv4,omitempty"`
	CidrIPv6            string                  `xml:"cidrIpv6,omitempty"`
	PrefixListID        string                  `xml:"prefixListId,omitempty"`
	ReferencedGroupInfo *referencedGroupInfoXML `xml:"referencedGroupInfo,omitempty"`
	Description         string                  `xml:"description,omitempty"`
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

// authorizeSecurityGroupResponseXML is the Authorize{Ingress,Egress} response.
// Its root element differs per op (Ingress vs Egress), so XMLName is set at
// runtime rather than fixed by a struct tag.
type authorizeSecurityGroupResponseXML struct {
	XMLName              xml.Name
	Xmlns                string                 `xml:"xmlns,attr"`
	RequestID            string                 `xml:"requestId"`
	Return               bool                   `xml:"return"`
	SecurityGroupRuleSet []securityGroupRuleXML `xml:"securityGroupRuleSet>item,omitempty"`
}

type describeSecurityGroupRulesResponseXML struct {
	XMLName              xml.Name               `xml:"DescribeSecurityGroupRulesResponse"`
	Xmlns                string                 `xml:"xmlns,attr"`
	RequestID            string                 `xml:"requestId"`
	SecurityGroupRuleSet []securityGroupRuleXML `xml:"securityGroupRuleSet>item"`
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
		writeCreateSGErr(w, err)
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

// writeCreateSGErr maps the provider's per-VPC name-uniqueness violation
// (surfaced as AlreadyExists) to the CreateSecurityGroup-only
// InvalidGroup.Duplicate code, falling back to the shared SG error mapping.
func writeCreateSGErr(w http.ResponseWriter, err error) {
	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidGroup.Duplicate", err.Error())
		return
	}

	writeSGErr(w, err)
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
		// The default security group is non-deletable: EC2 answers a distinct
		// Client.CannotDelete code rather than the generic DependencyViolation.
		if cerrors.IsFailedPrecondition(err) && strings.Contains(err.Error(), "CannotDelete:") {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "Client.CannotDelete", err.Error())
			return
		}

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
	case groupIDFilter:
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

// describeSecurityGroupRules flattens every group's ingress + egress rules into
// the SecurityGroupRule shape, honoring the group-id / security-group-rule-id
// filters and the SecurityGroupRuleId.N selector.
func (h *Handler) describeSecurityGroupRules(w http.ResponseWriter, r *http.Request) {
	filters := awsquery.Filters(r.Form)
	if err := validateSGRuleFilters(filters); err != nil {
		writeSGErr(w, err)
		return
	}

	sgs, err := h.vpc.DescribeSecurityGroups(r.Context(), nil)
	if err != nil {
		writeSGErr(w, err)
		return
	}

	wantGroups := sgRuleFilterValues(filters, groupIDFilter)
	wantRuleIDs := append(awsquery.ListStrings(r.Form, "SecurityGroupRuleId"),
		sgRuleFilterValues(filters, securityGroupRuleIDFilter)...)

	items := make([]securityGroupRuleXML, 0)

	for i := range sgs {
		sg := &sgs[i]
		if len(wantGroups) > 0 && !containsString(wantGroups, sg.ID) {
			continue
		}

		items = appendSGRuleXMLs(items, sg.ID, false, sg.IngressRules, wantRuleIDs)
		items = appendSGRuleXMLs(items, sg.ID, true, sg.EgressRules, wantRuleIDs)
	}

	awsquery.WriteXMLResponse(w, describeSecurityGroupRulesResponseXML{
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		SecurityGroupRuleSet: items,
	})
}

// appendSGRuleXMLs appends the rules to out as SecurityGroupRule items, keeping
// only those whose id is in wantRuleIDs when that selector is non-empty.
func appendSGRuleXMLs(
	out []securityGroupRuleXML,
	groupID string,
	egress bool,
	rules []netdriver.SecurityRule,
	wantRuleIDs []string,
) []securityGroupRuleXML {
	for i := range rules {
		if len(wantRuleIDs) > 0 && !containsString(wantRuleIDs, rules[i].RuleID) {
			continue
		}

		out = append(out, toSecurityGroupRuleXML(groupID, egress, &rules[i]))
	}

	return out
}

// sgRuleFilterValues returns the accumulated values of every filter with the
// given name.
func sgRuleFilterValues(filters []awsquery.Filter, name string) []string {
	var out []string

	for _, f := range filters {
		if f.Name == name {
			out = append(out, f.Values...)
		}
	}

	return out
}

// validateSGRuleFilters rejects DescribeSecurityGroupRules filter names this
// emulator does not implement, matching real EC2's InvalidParameterValue.
func validateSGRuleFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		switch f.Name {
		case groupIDFilter, securityGroupRuleIDFilter:
		default:
			return newInvalidParameterErr(fmt.Sprintf("The filter '%s' is invalid", f.Name))
		}
	}

	return nil
}

func (h *Handler) authorizeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	h.applyRules(w, r, ruleApply{
		apply:        h.vpc.AddEgressRule,
		responseName: "AuthorizeSecurityGroupEgressResponse",
		existing:     func(sg *netdriver.SecurityGroupInfo) []netdriver.SecurityRule { return sg.EgressRules },
		egress:       true,
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
	// egress marks the Authorize path as egress so the returned
	// SecurityGroupRule items carry isEgress=true.
	egress bool
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

	for i := range rules {
		if err := spec.apply(r.Context(), groupID, rules[i]); err != nil {
			writeSGErr(w, err)
			return
		}
	}

	// Authorize (existing != nil) echoes the created SecurityGroupRule set; the
	// idempotent Revoke path carries no payload.
	if spec.existing != nil {
		writeAuthorizeSGResponse(w, spec.responseName, groupID, spec.egress, rules)
		return
	}

	writeSimpleSGResponse(w, spec.responseName)
}

// writeAuthorizeSGResponse emits <return>true</return> plus the
// securityGroupRuleSet AWS returns from Authorize{Ingress,Egress}.
func writeAuthorizeSGResponse(w http.ResponseWriter, name, groupID string, egress bool, rules []netdriver.SecurityRule) {
	items := make([]securityGroupRuleXML, 0, len(rules))
	for i := range rules {
		items = append(items, toSecurityGroupRuleXML(groupID, egress, &rules[i]))
	}

	awsquery.WriteXMLResponse(w, authorizeSecurityGroupResponseXML{
		XMLName:              xml.Name{Local: name},
		Xmlns:                awsquery.Namespace,
		RequestID:            awsquery.RequestID,
		Return:               true,
		SecurityGroupRuleSet: items,
	})
}

// toSecurityGroupRuleXML maps one stored rule to the flat SecurityGroupRule
// wire shape, emitting exactly the target element (cidrIpv4/cidrIpv6/
// prefixListId/referencedGroupInfo) the rule carries.
func toSecurityGroupRuleXML(groupID string, egress bool, rule *netdriver.SecurityRule) securityGroupRuleXML {
	x := securityGroupRuleXML{
		SecurityGroupRuleID: rule.RuleID,
		GroupID:             groupID,
		GroupOwnerID:        ownerID,
		IsEgress:            egress,
		IPProtocol:          rule.Protocol,
		FromPort:            rule.FromPort,
		ToPort:              rule.ToPort,
		CidrIPv4:            rule.CIDR,
		CidrIPv6:            rule.IPv6CIDR,
		PrefixListID:        rule.PrefixListID,
		Description:         rule.Description,
	}

	if rule.ReferencedGroupID != "" {
		userID := rule.ReferencedGroupOwnerID
		if userID == "" {
			userID = ownerID
		}

		x.ReferencedGroupInfo = &referencedGroupInfoXML{GroupID: rule.ReferencedGroupID, UserID: userID}
	}

	return x
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

	for i := range rules {
		for j := range current {
			if current[j].Matches(&rules[i]) {
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
		rules = append(rules, rulesForPermission(form, base)...)
	}

	return rules
}

// rulesForPermission unrolls one IpPermissions.N block into one SecurityRule
// per target (IpRanges / Ipv6Ranges / PrefixListIds / Groups), minting an
// "sgr-" id for each. A block with no target yields a single target-less rule.
func rulesForPermission(form url.Values, base string) []netdriver.SecurityRule {
	proto := form.Get(base + ".IpProtocol")
	fromPort, _ := strconv.Atoi(form.Get(base + ".FromPort"))
	toPort, _ := strconv.Atoi(form.Get(base + ".ToPort"))

	mk := func(target netdriver.SecurityRule) netdriver.SecurityRule {
		target.Protocol = proto
		target.FromPort = fromPort
		target.ToPort = toPort
		target.RuleID = idgen.GenerateID("sgr-")

		return target
	}

	var out []netdriver.SecurityRule

	for _, e := range rangesFromNested(form, base+".IpRanges", "CidrIp") {
		out = append(out, mk(netdriver.SecurityRule{CIDR: e.value, Description: e.description}))
	}

	for _, e := range rangesFromNested(form, base+".Ipv6Ranges", "CidrIpv6") {
		out = append(out, mk(netdriver.SecurityRule{IPv6CIDR: e.value, Description: e.description}))
	}

	for _, e := range rangesFromNested(form, base+".PrefixListIds", "PrefixListId") {
		out = append(out, mk(netdriver.SecurityRule{PrefixListID: e.value, Description: e.description}))
	}

	for _, g := range groupsFromNested(form, base+".Groups") {
		out = append(out, mk(netdriver.SecurityRule{
			ReferencedGroupID:      g.groupID,
			ReferencedGroupOwnerID: g.ownerID,
			Description:            g.description,
		}))
	}

	if len(out) == 0 {
		out = append(out, mk(netdriver.SecurityRule{}))
	}

	return out
}

// rangeEntry is a single nested range/prefix-list entry with its description.
type rangeEntry struct {
	value       string
	description string
}

// rangesFromNested reads <prefix>.M.<valueField> (+ optional .Description) for
// the IpRanges / Ipv6Ranges / PrefixListIds groups.
func rangesFromNested(form url.Values, prefix, valueField string) []rangeEntry {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]rangeEntry, 0, len(indices))

	for _, idx := range indices {
		b := prefix + "." + strconv.Itoa(idx)
		if v := form.Get(b + "." + valueField); v != "" {
			out = append(out, rangeEntry{value: v, description: form.Get(b + ".Description")})
		}
	}

	return out
}

// referencedGroup is one parsed UserIdGroupPairs (Groups.M) source reference.
type referencedGroup struct {
	groupID     string
	ownerID     string
	description string
}

// groupsFromNested reads Groups.M.GroupId (+ optional .UserId / .Description).
func groupsFromNested(form url.Values, prefix string) []referencedGroup {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]referencedGroup, 0, len(indices))

	for _, idx := range indices {
		b := prefix + "." + strconv.Itoa(idx)

		gid := form.Get(b + ".GroupId")
		if gid == "" {
			continue
		}

		out = append(out, referencedGroup{
			groupID:     gid,
			ownerID:     form.Get(b + ".UserId"),
			description: form.Get(b + ".Description"),
		})
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

	for i := range rules {
		rule := &rules[i]
		k := key{protocol: rule.Protocol, from: rule.FromPort, to: rule.ToPort}

		perm, ok := byKey[k]
		if !ok {
			perm = &ipPermissionXML{
				IPProtocol: rule.Protocol,
				FromPort:   rule.FromPort,
				ToPort:     rule.ToPort,
			}
			byKey[k] = perm

			order = append(order, k)
		}

		addRuleTarget(perm, rule)
	}

	out := make([]ipPermissionXML, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}

	return out
}

// addRuleTarget appends the rule's single target (IPv4 / IPv6 / prefix list /
// referenced group) to the matching sub-list of the IpPermission.
func addRuleTarget(perm *ipPermissionXML, rule *netdriver.SecurityRule) {
	switch {
	case rule.CIDR != "":
		perm.IPRanges = append(perm.IPRanges, ipRangeXML{CidrIP: rule.CIDR, Description: rule.Description})
	case rule.IPv6CIDR != "":
		perm.IPv6Ranges = append(perm.IPv6Ranges, ipv6RangeXML{CidrIPv6: rule.IPv6CIDR, Description: rule.Description})
	case rule.PrefixListID != "":
		perm.PrefixListIDs = append(perm.PrefixListIDs,
			prefixListIDXML{PrefixListID: rule.PrefixListID, Description: rule.Description})
	case rule.ReferencedGroupID != "":
		userID := rule.ReferencedGroupOwnerID
		if userID == "" {
			userID = ownerID
		}

		perm.Groups = append(perm.Groups,
			userIDGroupPairXML{GroupID: rule.ReferencedGroupID, UserID: userID, Description: rule.Description})
	}
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
