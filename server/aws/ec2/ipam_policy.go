package ec2

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) ipamPolicy() (netdriver.IPAMPolicy, bool) {
	i, ok := h.vpc.(netdriver.IPAMPolicy)

	return i, ok
}

type ipamPolicyXML struct {
	IpamPolicyID  string    `xml:"ipamPolicyId"`
	IpamPolicyArn string    `xml:"ipamPolicyArn"`
	IpamID        string    `xml:"ipamId,omitempty"`
	IpamRegion    string    `xml:"ipamPolicyRegion,omitempty"`
	OwnerID       string    `xml:"ownerId,omitempty"`
	State         string    `xml:"state"`
	Tags          []tagItem `xml:"tagSet>item,omitempty"`
}

//nolint:gocyclo,dupl // flat action dispatch table
func (h *Handler) routeIPAMPolicy(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, ok := h.ipamPolicy()
	if !ok {
		return false
	}

	switch action {
	case "CreateIpamPolicy":
		h.createIpamPolicy(w, r, ip)
	case "DeleteIpamPolicy":
		h.deleteIpamPolicy(w, r, ip)
	case "DescribeIpamPolicies":
		h.describeIpamPolicies(w, r, ip)
	case "EnableIpamPolicy":
		h.enableIpamPolicy(w, r, ip)
	case "DisableIpamPolicy":
		h.disableIpamPolicy(w, r, ip)
	case "GetEnabledIpamPolicy":
		h.getEnabledIpamPolicy(w, r, ip)
	case "ModifyIpamPolicyAllocationRules":
		h.modifyIpamPolicyAllocationRules(w, r, ip)
	case "GetIpamPolicyAllocationRules":
		h.getIpamPolicyAllocationRules(w, r, ip)
	case "GetIpamPolicyOrganizationTargets":
		h.getIpamPolicyOrganizationTargets(w, r, ip)
	case "EnableIpamOrganizationAdminAccount":
		h.enableIpamOrgAdmin(w, r, ip)
	case "DisableIpamOrganizationAdminAccount":
		h.disableIpamOrgAdmin(w, r, ip)
	default:
		return false
	}

	return true
}

func (*Handler) createIpamPolicy(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	out, err := ip.CreateIpamPolicy(r.Context(), r.Form.Get("IpamId"), mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-policy"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPolicy(w, "CreateIpamPolicyResponse", out)
}

func (*Handler) deleteIpamPolicy(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	out, err := ip.DeleteIpamPolicy(r.Context(), r.Form.Get("IpamPolicyId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamPolicy(w, "DeleteIpamPolicyResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamPolicies(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	items, err := ip.DescribeIpamPolicies(r.Context(), awsquery.ListStrings(r.Form, "IpamPolicyId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamPolicyXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamPolicyXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name        `xml:"DescribeIpamPoliciesResponse"`
		Xmlns   string          `xml:"xmlns,attr"`
		Req     string          `xml:"requestId"`
		Set     []ipamPolicyXML `xml:"ipamPolicySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) enableIpamPolicy(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	id, err := ip.EnableIpamPolicy(r.Context(), r.Form.Get("IpamPolicyId"), r.Form.Get("OrganizationTargetId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName      xml.Name `xml:"EnableIpamPolicyResponse"`
		Xmlns        string   `xml:"xmlns,attr"`
		Req          string   `xml:"requestId"`
		IpamPolicyID string   `xml:"ipamPolicyId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, IpamPolicyID: id})
}

func (*Handler) disableIpamPolicy(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	if err := ip.DisableIpamPolicy(r.Context(), r.Form.Get("IpamPolicyId")); err != nil {
		writeIPAMErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DisableIpamPolicyResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Return  bool     `xml:"return"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Return: true})
}

func (*Handler) getEnabledIpamPolicy(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	id, enabled, managedBy, err := ip.GetEnabledIpamPolicy(r.Context())
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName           xml.Name `xml:"GetEnabledIpamPolicyResponse"`
		Xmlns             string   `xml:"xmlns,attr"`
		Req               string   `xml:"requestId"`
		IpamPolicyID      string   `xml:"ipamPolicyId,omitempty"`
		IpamPolicyEnabled bool     `xml:"ipamPolicyEnabled"`
		ManagedBy         string   `xml:"managedBy,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, IpamPolicyID: id, IpamPolicyEnabled: enabled, ManagedBy: managedBy})
}

// allocationRuleXML is one <allocationRuleSet><item>; matches the SDK's
// IpamPolicyAllocationRule (sourceIpamPoolId only).
type allocationRuleXML struct {
	SourceIpamPoolID string `xml:"sourceIpamPoolId,omitempty"`
}

// ipamPolicyDocumentXML matches types.IpamPolicyDocument: an allocationRuleSet
// plus the ipamPolicyId/locale/resourceType the rules are scoped to.
type ipamPolicyDocumentXML struct {
	IpamPolicyID   string              `xml:"ipamPolicyId,omitempty"`
	Locale         string              `xml:"locale,omitempty"`
	ResourceType   string              `xml:"resourceType,omitempty"`
	AllocationRule []allocationRuleXML `xml:"allocationRuleSet>item,omitempty"`
}

// parseAllocationRules reads the request's flattened AllocationRule.N list.
func parseAllocationRules(form url.Values) []netdriver.IpamAllocationRule {
	idx := awsquery.CollectIndices(form, "AllocationRule")
	rules := make([]netdriver.IpamAllocationRule, 0, len(idx))

	for _, i := range idx {
		pool := form.Get(fmt.Sprintf("AllocationRule.%d.SourceIpamPoolId", i))
		rules = append(rules, netdriver.IpamAllocationRule{SourceIpamPoolID: pool})
	}

	return rules
}

func toIpamPolicyDocumentXML(p *netdriver.IpamPolicy) ipamPolicyDocumentXML {
	doc := ipamPolicyDocumentXML{
		IpamPolicyID: p.ID,
		Locale:       p.Locale,
		ResourceType: p.ResourceType,
	}

	for _, rule := range p.AllocationRules {
		doc.AllocationRule = append(doc.AllocationRule, allocationRuleXML{SourceIpamPoolID: rule.SourceIpamPoolID})
	}

	return doc
}

func (*Handler) modifyIpamPolicyAllocationRules(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	out, err := ip.ModifyIpamPolicyAllocationRules(r.Context(),
		r.Form.Get("IpamPolicyId"), r.Form.Get("Locale"), r.Form.Get("ResourceType"),
		parseAllocationRules(r.Form))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	// The SDK output carries IpamPolicyDocument (element ipamPolicyDocument),
	// not a Return field — emit the modified document so the caller sees it.
	awsquery.WriteXMLResponse(w, struct {
		XMLName  xml.Name              `xml:"ModifyIpamPolicyAllocationRulesResponse"`
		Xmlns    string                `xml:"xmlns,attr"`
		Req      string                `xml:"requestId"`
		Document ipamPolicyDocumentXML `xml:"ipamPolicyDocument"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Document: toIpamPolicyDocumentXML(out)})
}

func (*Handler) getIpamPolicyAllocationRules(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	p, err := ip.GetIpamPolicyAllocationRules(r.Context(), r.Form.Get("IpamPolicyId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	// Output is IpamPolicyDocuments []IpamPolicyDocument + NextToken. The
	// emulator models one document (the policy's rules) per policy.
	awsquery.WriteXMLResponse(w, struct {
		XMLName   xml.Name                `xml:"GetIpamPolicyAllocationRulesResponse"`
		Xmlns     string                  `xml:"xmlns,attr"`
		Req       string                  `xml:"requestId"`
		Set       []ipamPolicyDocumentXML `xml:"ipamPolicyDocumentSet>item"`
		NextToken string                  `xml:"nextToken,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: []ipamPolicyDocumentXML{toIpamPolicyDocumentXML(p)}})
}

func (*Handler) getIpamPolicyOrganizationTargets(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	targets, err := ip.GetIpamPolicyOrganizationTargets(r.Context(), r.Form.Get("IpamPolicyId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type targetXML struct {
		OrganizationTargetID string `xml:"organizationTargetId"`
	}

	out := make([]targetXML, 0, len(targets))
	for _, t := range targets {
		out = append(out, targetXML{OrganizationTargetID: t})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name    `xml:"GetIpamPolicyOrganizationTargetsResponse"`
		Xmlns   string      `xml:"xmlns,attr"`
		Req     string      `xml:"requestId"`
		Set     []targetXML `xml:"organizationTargetSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) enableIpamOrgAdmin(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	ok, err := ip.EnableIpamOrganizationAdminAccount(r.Context(), r.Form.Get("DelegatedAdminAccountId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamOrgAdminResult(w, "EnableIpamOrganizationAdminAccountResponse", ok)
}

func (*Handler) disableIpamOrgAdmin(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPolicy) {
	ok, err := ip.DisableIpamOrganizationAdminAccount(r.Context(), r.Form.Get("DelegatedAdminAccountId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamOrgAdminResult(w, "DisableIpamOrganizationAdminAccountResponse", ok)
}

func writeIpamOrgAdminResult(w http.ResponseWriter, root string, success bool) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:""`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Success bool     `xml:"success"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Success: success})
}

func writeIpamPolicy(w http.ResponseWriter, root string, out *netdriver.IpamPolicy) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name      `xml:""`
		Xmlns   string        `xml:"xmlns,attr"`
		Req     string        `xml:"requestId"`
		Policy  ipamPolicyXML `xml:"ipamPolicy"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Policy: toIpamPolicyXML(out)})
}

func toIpamPolicyXML(p *netdriver.IpamPolicy) ipamPolicyXML {
	return ipamPolicyXML{
		IpamPolicyID: p.ID, IpamPolicyArn: p.ARN, IpamID: p.IpamID, IpamRegion: p.IpamRegion,
		OwnerID: p.OwnerID, State: p.State, Tags: toTagItems(p.Tags),
	}
}
