package elbv2

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// writeUnsupported reports that the backing driver does not implement the
// extension interface an operation needs. It maps to a generic ELBv2 error so
// the SDK sees a real API error rather than a silent success.
func writeUnsupported(w http.ResponseWriter, op string) {
	awsquery.WriteXMLError(w, http.StatusBadRequest,
		"UnsupportedProtocol", "operation not supported by this backend: "+op)
}

// --- ModifyListener ---

func (h *Handler) modifyListener(w http.ResponseWriter, r *http.Request) {
	form := r.Form
	arn := form.Get("ListenerArn")

	input := lbdriver.ModifyListenerInput{
		ListenerARN:    arn,
		Port:           formInt(form.Get("Port")),
		Protocol:       form.Get("Protocol"),
		DefaultActions: parseActions(form, "DefaultActions.member"),
	}

	if err := h.lb.ModifyListener(r.Context(), input); err != nil {
		writeErr(w, err)
		return
	}

	getter, ok := h.lb.(lbdriver.ListenerGetter)
	if !ok {
		writeUnsupported(w, "ModifyListener")
		return
	}

	li, err := getter.GetListener(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyListenerResponse{
		Xmlns:    Namespace,
		Result:   listenersResult{Listeners: listenersXML{Member: []listenerXML{toListenerXML(li)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- ModifyTargetGroup ---

func (h *Handler) modifyTargetGroup(w http.ResponseWriter, r *http.Request) {
	mod, ok := h.lb.(lbdriver.TargetGroupModifier)
	if !ok {
		writeUnsupported(w, "ModifyTargetGroup")
		return
	}

	form := r.Form

	input := lbdriver.ModifyTargetGroupInput{
		TargetGroupARN:     form.Get("TargetGroupArn"),
		HealthCheckProto:   form.Get("HealthCheckProtocol"),
		HealthCheckPort:    form.Get("HealthCheckPort"),
		HealthCheckPath:    form.Get("HealthCheckPath"),
		IntervalSeconds:    formInt(form.Get("HealthCheckIntervalSeconds")),
		TimeoutSeconds:     formInt(form.Get("HealthCheckTimeoutSeconds")),
		HealthyThreshold:   formInt(form.Get("HealthyThresholdCount")),
		UnhealthyThreshold: formInt(form.Get("UnhealthyThresholdCount")),
		Matcher:            form.Get("Matcher.HttpCode"),
	}

	tg, err := mod.ModifyTargetGroup(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyTargetGroupResponse{
		Xmlns:    Namespace,
		Result:   targetGroupsResult{TargetGroups: targetGroupsXML{Member: []targetGroupXML{toTargetGroupXML(tg)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- ModifyRule ---

func (h *Handler) modifyRule(w http.ResponseWriter, r *http.Request) {
	mod, ok := h.lb.(lbdriver.RuleModifier)
	if !ok {
		writeUnsupported(w, "ModifyRule")
		return
	}

	form := r.Form

	rule, err := mod.ModifyRule(r.Context(), lbdriver.ModifyRuleInput{
		RuleARN:    form.Get("RuleArn"),
		Conditions: parseConditions(form, "Conditions.member"),
		Actions:    parseActions(form, "Actions.member"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyRuleResponse{
		Xmlns:    Namespace,
		Result:   rulesResult{Rules: rulesXML{Member: []ruleXML{toRuleXML(rule)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- SetRulePriorities ---

func (h *Handler) setRulePriorities(w http.ResponseWriter, r *http.Request) {
	mod, ok := h.lb.(lbdriver.RuleModifier)
	if !ok {
		writeUnsupported(w, "SetRulePriorities")
		return
	}

	pairs := parseRulePriorities(r.Form)

	rules, err := mod.SetRulePriorities(r.Context(), pairs)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := rulesXML{Member: make([]ruleXML, 0, len(rules))}
	for i := range rules {
		out.Member = append(out.Member, toRuleXML(&rules[i]))
	}

	awsquery.WriteXMLResponse(w, setRulePrioritiesResponse{
		Xmlns:    Namespace,
		Result:   rulesResult{Rules: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseRulePriorities parses the RulePriorities.member.N.{RuleArn,Priority}
// list into driver pairs.
func parseRulePriorities(form url.Values) []lbdriver.RulePriorityPair {
	indices := awsquery.CollectIndices(form, "RulePriorities.member")
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.RulePriorityPair, 0, len(indices))
	for _, n := range indices {
		base := "RulePriorities.member." + strconv.Itoa(n)
		out = append(out, lbdriver.RulePriorityPair{
			RuleARN:  form.Get(base + ".RuleArn"),
			Priority: formInt(form.Get(base + ".Priority")),
		})
	}

	return out
}

// --- SetSecurityGroups ---

func (h *Handler) setSecurityGroups(w http.ResponseWriter, r *http.Request) {
	mod, ok := h.lb.(lbdriver.LBNetworkModifier)
	if !ok {
		writeUnsupported(w, "SetSecurityGroups")
		return
	}

	sgs := awsquery.ListStrings(r.Form, "SecurityGroups.member")

	if err := mod.SetSecurityGroups(r.Context(), r.Form.Get("LoadBalancerArn"), sgs); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, setSecurityGroupsResponse{
		Xmlns:    Namespace,
		Result:   setSecurityGroupsResult{SecurityGroupIds: &securityGroupsXML{Member: sgs}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- SetSubnets ---

func (h *Handler) setSubnets(w http.ResponseWriter, r *http.Request) {
	mod, ok := h.lb.(lbdriver.LBNetworkModifier)
	if !ok {
		writeUnsupported(w, "SetSubnets")
		return
	}

	subnets := awsquery.ListStrings(r.Form, "Subnets.member")

	got, err := mod.SetSubnets(r.Context(), r.Form.Get("LoadBalancerArn"), subnets)
	if err != nil {
		writeErr(w, err)
		return
	}

	az := &availabilityZonesXML{}
	for _, s := range got {
		az.Member = append(az.Member, availabilityZoneXML{ZoneName: zoneNameForSubnet(), SubnetID: s})
	}

	awsquery.WriteXMLResponse(w, setSubnetsResponse{
		Xmlns:    Namespace,
		Result:   setSubnetsResult{AvailabilityZones: az},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- response envelopes ---

type modifyListenerResponse struct {
	XMLName  xml.Name         `xml:"ModifyListenerResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listenersResult  `xml:"ModifyListenerResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type modifyTargetGroupResponse struct {
	XMLName  xml.Name           `xml:"ModifyTargetGroupResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   targetGroupsResult `xml:"ModifyTargetGroupResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type modifyRuleResponse struct {
	XMLName  xml.Name         `xml:"ModifyRuleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   rulesResult      `xml:"ModifyRuleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type setRulePrioritiesResponse struct {
	XMLName  xml.Name         `xml:"SetRulePrioritiesResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   rulesResult      `xml:"SetRulePrioritiesResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type setSecurityGroupsResult struct {
	SecurityGroupIds *securityGroupsXML `xml:"SecurityGroupIds,omitempty"`
}

type setSecurityGroupsResponse struct {
	XMLName  xml.Name                `xml:"SetSecurityGroupsResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   setSecurityGroupsResult `xml:"SetSecurityGroupsResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type setSubnetsResult struct {
	AvailabilityZones *availabilityZonesXML `xml:"AvailabilityZones,omitempty"`
}

type setSubnetsResponse struct {
	XMLName  xml.Name         `xml:"SetSubnetsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   setSubnetsResult `xml:"SetSubnetsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}
