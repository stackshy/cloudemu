package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type networkACLEntryXML struct {
	RuleNumber int    `xml:"ruleNumber"`
	Protocol   string `xml:"protocol"`
	RuleAction string `xml:"ruleAction"`
	Egress     bool   `xml:"egress"`
	CIDRBlock  string `xml:"cidrBlock,omitempty"`
	PortRange  *struct {
		From int `xml:"from"`
		To   int `xml:"to"`
	} `xml:"portRange,omitempty"`
}

type networkACLAssociationXML struct {
	NetworkACLAssociationID string `xml:"networkAclAssociationId"`
	NetworkACLID            string `xml:"networkAclId"`
	SubnetID                string `xml:"subnetId"`
}

type networkACLXML struct {
	NetworkACLID string                     `xml:"networkAclId"`
	VpcID        string                     `xml:"vpcId"`
	OwnerID      string                     `xml:"ownerId"`
	IsDefault    bool                       `xml:"default"`
	Entries      []networkACLEntryXML       `xml:"entrySet>item,omitempty"`
	Associations []networkACLAssociationXML `xml:"associationSet>item,omitempty"`
	Tags         []tagItem                  `xml:"tagSet>item,omitempty"`
}

type replaceNetworkACLAssociationResponseXML struct {
	XMLName          xml.Name `xml:"ReplaceNetworkAclAssociationResponse"`
	Xmlns            string   `xml:"xmlns,attr"`
	RequestID        string   `xml:"requestId"`
	NewAssociationID string   `xml:"newAssociationId"`
}

type createNetworkACLResponseXML struct {
	XMLName    xml.Name      `xml:"CreateNetworkAclResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	NetworkACL networkACLXML `xml:"networkAcl"`
}

type describeNetworkACLsResponseXML struct {
	XMLName       xml.Name        `xml:"DescribeNetworkAclsResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	NetworkACLSet []networkACLXML `xml:"networkAclSet>item"`
	NextToken     string          `xml:"nextToken,omitempty"`
}

type deleteNetworkACLResponseXML struct {
	XMLName   xml.Name `xml:"DeleteNetworkAclResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createNetworkACLEntryResponseXML struct {
	XMLName   xml.Name `xml:"CreateNetworkAclEntryResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteNetworkACLEntryResponseXML struct {
	XMLName   xml.Name `xml:"DeleteNetworkAclEntryResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type replaceNetworkACLEntryResponseXML struct {
	XMLName   xml.Name `xml:"ReplaceNetworkAclEntryResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createNetworkACL(w http.ResponseWriter, r *http.Request) {
	tags := mergeTagSpecs(awsquery.TagSpecs(r.Form), "network-acl")

	acl, err := h.vpc.CreateNetworkACL(r.Context(), r.Form.Get("VpcId"), tags)
	if err != nil {
		writeNetworkACLErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createNetworkACLResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		NetworkACL: h.toNetworkACLXML(acl),
	})
}

func (h *Handler) deleteNetworkACL(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteNetworkACL(r.Context(), r.Form.Get("NetworkAclId")); err != nil {
		writeNetworkACLErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteNetworkACLResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe+filter pattern, mirrors describeNatGateways
func (h *Handler) describeNetworkACLs(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "NetworkAclId")

	acls, err := h.vpc.DescribeNetworkACLs(r.Context(), ids)
	if err != nil {
		writeNetworkACLErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateNetworkingFilters(filters, naclFilterMatch); err != nil {
		writeNetworkACLErr(w, err)
		return
	}

	out := filterXML(acls, filters, naclMatchesFilters, h.toNetworkACLXML)
	page, next := pageNetworkingXML(out, r, func(a networkACLXML) string { return a.NetworkACLID })

	awsquery.WriteXMLResponse(w, describeNetworkACLsResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		NetworkACLSet: page,
		NextToken:     next,
	})
}

func (h *Handler) createNetworkACLEntry(w http.ResponseWriter, r *http.Request) {
	ruleNum, _ := strconv.Atoi(r.Form.Get("RuleNumber"))
	fromPort, _ := strconv.Atoi(r.Form.Get("PortRange.From"))
	toPort, _ := strconv.Atoi(r.Form.Get("PortRange.To"))
	egress := r.Form.Get("Egress") == formTrue

	rule := &netdriver.NetworkACLRule{
		RuleNumber: ruleNum,
		Protocol:   r.Form.Get("Protocol"),
		Action:     r.Form.Get("RuleAction"),
		CIDR:       r.Form.Get("CidrBlock"),
		FromPort:   fromPort,
		ToPort:     toPort,
		Egress:     egress,
	}

	if err := h.vpc.AddNetworkACLRule(r.Context(), r.Form.Get("NetworkAclId"), rule); err != nil {
		writeNetworkACLEntryErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createNetworkACLEntryResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// replaceNetworkACLEntry swaps the rule at (RuleNumber, Egress) for the new one
// the caller supplied. Real EC2 requires the entry to already exist, so the
// remove step's NotFound is the correct error when it does not. Modeled as
// remove-then-add because the driver keys rules by number and has no in-place
// replace.
func (h *Handler) replaceNetworkACLEntry(w http.ResponseWriter, r *http.Request) {
	ruleNum, _ := strconv.Atoi(r.Form.Get("RuleNumber"))
	fromPort, _ := strconv.Atoi(r.Form.Get("PortRange.From"))
	toPort, _ := strconv.Atoi(r.Form.Get("PortRange.To"))
	egress := r.Form.Get("Egress") == formTrue
	aclID := r.Form.Get("NetworkAclId")

	if err := h.vpc.RemoveNetworkACLRule(r.Context(), aclID, ruleNum, egress); err != nil {
		writeNetworkACLEntryErr(w, err)
		return
	}

	rule := &netdriver.NetworkACLRule{
		RuleNumber: ruleNum,
		Protocol:   r.Form.Get("Protocol"),
		Action:     r.Form.Get("RuleAction"),
		CIDR:       r.Form.Get("CidrBlock"),
		FromPort:   fromPort,
		ToPort:     toPort,
		Egress:     egress,
	}

	if err := h.vpc.AddNetworkACLRule(r.Context(), aclID, rule); err != nil {
		writeNetworkACLEntryErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, replaceNetworkACLEntryResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteNetworkACLEntry(w http.ResponseWriter, r *http.Request) {
	ruleNum, _ := strconv.Atoi(r.Form.Get("RuleNumber"))
	egress := r.Form.Get("Egress") == formTrue

	err := h.vpc.RemoveNetworkACLRule(r.Context(),
		r.Form.Get("NetworkAclId"), ruleNum, egress)
	if err != nil {
		writeNetworkACLEntryErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteNetworkACLEntryResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// replaceNetworkACLAssociation moves a subnet from its current network ACL to
// the one named, returning a fresh association id. Subnet<->ACL associations are
// an AWS-only concept, so the backend serves it via the optional
// NetworkACLAssociator interface.
func (h *Handler) replaceNetworkACLAssociation(w http.ResponseWriter, r *http.Request) {
	assoc, ok := h.vpc.(netdriver.NetworkACLAssociator)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"ReplaceNetworkAclAssociation is not supported")

		return
	}

	out, err := assoc.ReplaceNetworkACLAssociation(r.Context(),
		r.Form.Get("AssociationId"), r.Form.Get("NetworkAclId"))
	if err != nil {
		writeNetworkACLAssocErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, replaceNetworkACLAssociationResponseXML{
		Xmlns:            awsquery.Namespace,
		RequestID:        awsquery.RequestID,
		NewAssociationID: out.ID,
	})
}

// writeNetworkACLAssocErr maps the two distinct not-found cases to the codes
// real EC2 uses: a missing NetworkAclId -> InvalidNetworkAclID.NotFound, a
// missing AssociationId -> InvalidAssociationID.NotFound.
func writeNetworkACLAssocErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsInvalidArgument(err):
		// Cross-VPC association attempt.
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	case cerrors.IsNotFound(err) && !strings.Contains(err.Error(), "network ACL association"):
		// A missing ACL, not a missing association. Matching the contiguous fixed
		// phrase "network ACL association" is robust: the ACL-not-found message is
		// `network ACL "<id>" not found`, so a caller-supplied id containing
		// "association" cannot forge the phrase (the quote breaks it).
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidNetworkAclID.NotFound", err.Error())
	default:
		writeErrWithNotFound(w, err, "InvalidAssociationID.NotFound", "DependencyViolation")
	}
}

func (h *Handler) toNetworkACLXML(a *netdriver.NetworkACL) networkACLXML {
	x := networkACLXML{
		NetworkACLID: a.ID,
		VpcID:        a.VPCID,
		OwnerID:      h.accountID,
		IsDefault:    a.IsDefault,
		Tags:         toTagItems(a.Tags),
	}

	for _, assoc := range a.Associations {
		x.Associations = append(x.Associations, networkACLAssociationXML{
			NetworkACLAssociationID: assoc.ID,
			NetworkACLID:            assoc.NetworkACLID,
			SubnetID:                assoc.SubnetID,
		})
	}

	for _, rule := range a.Rules {
		entry := networkACLEntryXML{
			RuleNumber: rule.RuleNumber,
			Protocol:   rule.Protocol,
			RuleAction: rule.Action,
			Egress:     rule.Egress,
			CIDRBlock:  rule.CIDR,
		}

		if rule.FromPort != 0 || rule.ToPort != 0 {
			entry.PortRange = &struct {
				From int `xml:"from"`
				To   int `xml:"to"`
			}{From: rule.FromPort, To: rule.ToPort}
		}

		x.Entries = append(x.Entries, entry)
	}

	return x
}

func naclMatchesFilters(a *netdriver.NetworkACL, filters []awsquery.Filter) bool {
	return matchNetworkingFilters(a, filters, naclFilterMatch)
}

// naclFilterMatch reports whether a satisfies filter f and whether f is a filter
// DescribeNetworkAcls recognizes. Terraform's aws_network_acl data source looks a
// network ACL up by vpc-id / association.subnet-id and expects only the matching
// ACL back; without honoring the filter every ACL in the account is returned.
func naclFilterMatch(a *netdriver.NetworkACL, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case "network-acl-id":
		return containsString(f.Values, a.ID), true
	case filterVPCID:
		return containsString(f.Values, a.VPCID), true
	case "default":
		return containsString(f.Values, boolFilterValue(a.IsDefault)), true
	case filterAssocSubnetID:
		for i := range a.Associations {
			if containsString(f.Values, a.Associations[i].SubnetID) {
				return true, true
			}
		}

		return false, true
	default:
		return tagFilterMatch(f.Name, f.Values, a.Tags)
	}
}

func writeNetworkACLErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidNetworkAclID.NotFound", "DependencyViolation")
}

// writeNetworkACLEntryErr maps the per-entry actions (create/delete/replace).
// The ACL and the entry are distinct resources: a missing entry on an existing
// ACL is InvalidNetworkAclEntry.NotFound (not InvalidNetworkAclID.NotFound), and
// a duplicate (ruleNumber, egress) is NetworkAclEntryAlreadyExists (not the
// generic ResourceAlreadyExists). A missing ACL still maps through
// writeNetworkACLErr to InvalidNetworkAclID.NotFound.
func writeNetworkACLEntryErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err) && strings.Contains(err.Error(), "not found in network ACL"):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidNetworkAclEntry.NotFound", cerrors.Message(err))
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "NetworkAclEntryAlreadyExists", cerrors.Message(err))
	default:
		writeNetworkACLErr(w, err)
	}
}
