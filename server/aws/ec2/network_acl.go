package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
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

type networkACLXML struct {
	NetworkACLID string               `xml:"networkAclId"`
	VpcID        string               `xml:"vpcId"`
	IsDefault    bool                 `xml:"default"`
	Entries      []networkACLEntryXML `xml:"entrySet>item,omitempty"`
	Tags         []tagItem            `xml:"tagSet>item,omitempty"`
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
		NetworkACL: toNetworkACLXML(acl),
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

//nolint:dupl // per-resource describe pattern
func (h *Handler) describeNetworkACLs(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "NetworkAclId")

	acls, err := h.vpc.DescribeNetworkACLs(r.Context(), ids)
	if err != nil {
		writeNetworkACLErr(w, err)
		return
	}

	out := make([]networkACLXML, 0, len(acls))
	for i := range acls {
		out = append(out, toNetworkACLXML(&acls[i]))
	}

	awsquery.WriteXMLResponse(w, describeNetworkACLsResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		NetworkACLSet: out,
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
		writeNetworkACLErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createNetworkACLEntryResponseXML{
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
		writeNetworkACLErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteNetworkACLEntryResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func toNetworkACLXML(a *netdriver.NetworkACL) networkACLXML {
	x := networkACLXML{
		NetworkACLID: a.ID,
		VpcID:        a.VPCID,
		IsDefault:    a.IsDefault,
		Tags:         toTagItems(a.Tags),
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

func writeNetworkACLErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidNetworkAclID.NotFound", "DependencyViolation")
}
