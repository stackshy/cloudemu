package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) trafficMirroring() (netdriver.TrafficMirroring, bool) {
	t, ok := h.vpc.(netdriver.TrafficMirroring)

	return t, ok
}

func (h *Handler) routeTrafficMirroring(w http.ResponseWriter, r *http.Request, action string) bool {
	t, ok := h.trafficMirroring()
	if !ok {
		return false
	}

	if h.routeTrafficMirrorTargets(w, r, action, t) {
		return true
	}

	if h.routeTrafficMirrorFilters(w, r, action, t) {
		return true
	}

	return h.routeTrafficMirrorSessions(w, r, action, t)
}

func (h *Handler) routeTrafficMirrorTargets(
	w http.ResponseWriter, r *http.Request, action string, t netdriver.TrafficMirroring,
) bool {
	switch action {
	case "CreateTrafficMirrorTarget":
		h.createTrafficMirrorTarget(w, r, t)
	case "DeleteTrafficMirrorTarget":
		h.deleteTrafficMirrorTarget(w, r, t)
	case "DescribeTrafficMirrorTargets":
		h.describeTrafficMirrorTargets(w, r, t)
	default:
		return false
	}

	return true
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (h *Handler) routeTrafficMirrorFilters(
	w http.ResponseWriter, r *http.Request, action string, t netdriver.TrafficMirroring,
) bool {
	switch action {
	case "CreateTrafficMirrorFilter":
		h.createTrafficMirrorFilter(w, r, t)
	case "DeleteTrafficMirrorFilter":
		h.deleteTrafficMirrorFilter(w, r, t)
	case "DescribeTrafficMirrorFilters":
		h.describeTrafficMirrorFilters(w, r, t)
	case "ModifyTrafficMirrorFilterNetworkServices":
		h.modifyTrafficMirrorFilterNetworkServices(w, r, t)
	case "CreateTrafficMirrorFilterRule":
		h.createTrafficMirrorFilterRule(w, r, t)
	case "ModifyTrafficMirrorFilterRule":
		h.modifyTrafficMirrorFilterRule(w, r, t)
	case "DeleteTrafficMirrorFilterRule":
		h.deleteTrafficMirrorFilterRule(w, r, t)
	case "DescribeTrafficMirrorFilterRules":
		h.describeTrafficMirrorFilterRules(w, r, t)
	default:
		return false
	}

	return true
}

func (h *Handler) routeTrafficMirrorSessions(
	w http.ResponseWriter, r *http.Request, action string, t netdriver.TrafficMirroring,
) bool {
	switch action {
	case "CreateTrafficMirrorSession":
		h.createTrafficMirrorSession(w, r, t)
	case "ModifyTrafficMirrorSession":
		h.modifyTrafficMirrorSession(w, r, t)
	case "DeleteTrafficMirrorSession":
		h.deleteTrafficMirrorSession(w, r, t)
	case "DescribeTrafficMirrorSessions":
		h.describeTrafficMirrorSessions(w, r, t)
	default:
		return false
	}

	return true
}

// ---- XML shapes ----

type trafficMirrorTargetXML struct {
	TrafficMirrorTargetID         string    `xml:"trafficMirrorTargetId"`
	NetworkInterfaceID            string    `xml:"networkInterfaceId,omitempty"`
	NetworkLoadBalancerArn        string    `xml:"networkLoadBalancerArn,omitempty"`
	GatewayLoadBalancerEndpointID string    `xml:"gatewayLoadBalancerEndpointId,omitempty"`
	Type                          string    `xml:"type,omitempty"`
	Description                   string    `xml:"description,omitempty"`
	OwnerID                       string    `xml:"ownerId,omitempty"`
	Tags                          []tagItem `xml:"tagSet>item,omitempty"`
}

type trafficMirrorPortRangeXML struct {
	FromPort int32 `xml:"fromPort"`
	ToPort   int32 `xml:"toPort"`
}

type trafficMirrorFilterRuleXML struct {
	TrafficMirrorFilterRuleID string                     `xml:"trafficMirrorFilterRuleId"`
	TrafficMirrorFilterID     string                     `xml:"trafficMirrorFilterId"`
	TrafficDirection          string                     `xml:"trafficDirection,omitempty"`
	RuleNumber                int32                      `xml:"ruleNumber"`
	RuleAction                string                     `xml:"ruleAction,omitempty"`
	Protocol                  int32                      `xml:"protocol,omitempty"`
	DestinationCidrBlock      string                     `xml:"destinationCidrBlock,omitempty"`
	SourceCidrBlock           string                     `xml:"sourceCidrBlock,omitempty"`
	DestinationPortRange      *trafficMirrorPortRangeXML `xml:"destinationPortRange,omitempty"`
	SourcePortRange           *trafficMirrorPortRangeXML `xml:"sourcePortRange,omitempty"`
	Description               string                     `xml:"description,omitempty"`
}

type trafficMirrorFilterXML struct {
	TrafficMirrorFilterID string                       `xml:"trafficMirrorFilterId"`
	Description           string                       `xml:"description,omitempty"`
	IngressFilterRules    []trafficMirrorFilterRuleXML `xml:"ingressFilterRuleSet>item,omitempty"`
	EgressFilterRules     []trafficMirrorFilterRuleXML `xml:"egressFilterRuleSet>item,omitempty"`
	NetworkServices       []string                     `xml:"networkServiceSet>item,omitempty"`
	Tags                  []tagItem                    `xml:"tagSet>item,omitempty"`
}

type trafficMirrorSessionXML struct {
	TrafficMirrorSessionID string    `xml:"trafficMirrorSessionId"`
	TrafficMirrorTargetID  string    `xml:"trafficMirrorTargetId"`
	TrafficMirrorFilterID  string    `xml:"trafficMirrorFilterId"`
	NetworkInterfaceID     string    `xml:"networkInterfaceId,omitempty"`
	PacketLength           int32     `xml:"packetLength,omitempty"`
	SessionNumber          int32     `xml:"sessionNumber"`
	VirtualNetworkID       int32     `xml:"virtualNetworkId"`
	Description            string    `xml:"description,omitempty"`
	OwnerID                string    `xml:"ownerId,omitempty"`
	Tags                   []tagItem `xml:"tagSet>item,omitempty"`
}

// ---- Target handlers ----

func (*Handler) createTrafficMirrorTarget(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	out, err := t.CreateTrafficMirrorTarget(r.Context(), netdriver.TrafficMirrorTargetConfig{
		Description:                   r.Form.Get("Description"),
		NetworkInterfaceID:            r.Form.Get("NetworkInterfaceId"),
		NetworkLoadBalancerARN:        r.Form.Get("NetworkLoadBalancerArn"),
		GatewayLoadBalancerEndpointID: r.Form.Get("GatewayLoadBalancerEndpointId"),
		Tags:                          mergeTagSpecs(awsquery.TagSpecs(r.Form), "traffic-mirror-target"),
	})
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"CreateTrafficMirrorTargetResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Target  trafficMirrorTargetXML `xml:"trafficMirrorTarget"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Target: toTrafficMirrorTargetXML(out)})
}

func (*Handler) deleteTrafficMirrorTarget(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	id := r.Form.Get("TrafficMirrorTargetId")
	if err := t.DeleteTrafficMirrorTarget(r.Context(), id); err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteTrafficMirrorTargetResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"trafficMirrorTargetId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeTrafficMirrorTargets(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	items, err := t.DescribeTrafficMirrorTargets(r.Context(), awsquery.ListStrings(r.Form, "TrafficMirrorTargetId"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	out := make([]trafficMirrorTargetXML, 0, len(items))
	for i := range items {
		out = append(out, toTrafficMirrorTargetXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                 `xml:"DescribeTrafficMirrorTargetsResponse"`
		Xmlns   string                   `xml:"xmlns,attr"`
		Req     string                   `xml:"requestId"`
		Set     []trafficMirrorTargetXML `xml:"trafficMirrorTargetSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

// ---- Filter handlers ----

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) createTrafficMirrorFilter(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	out, err := t.CreateTrafficMirrorFilter(r.Context(), r.Form.Get("Description"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "traffic-mirror-filter"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"CreateTrafficMirrorFilterResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Filter  trafficMirrorFilterXML `xml:"trafficMirrorFilter"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Filter: toTrafficMirrorFilterXML(out)})
}

func (*Handler) deleteTrafficMirrorFilter(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	id := r.Form.Get("TrafficMirrorFilterId")
	if err := t.DeleteTrafficMirrorFilter(r.Context(), id); err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteTrafficMirrorFilterResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"trafficMirrorFilterId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeTrafficMirrorFilters(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	items, err := t.DescribeTrafficMirrorFilters(r.Context(), awsquery.ListStrings(r.Form, "TrafficMirrorFilterId"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	out := make([]trafficMirrorFilterXML, 0, len(items))
	for i := range items {
		out = append(out, toTrafficMirrorFilterXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                 `xml:"DescribeTrafficMirrorFiltersResponse"`
		Xmlns   string                   `xml:"xmlns,attr"`
		Req     string                   `xml:"requestId"`
		Set     []trafficMirrorFilterXML `xml:"trafficMirrorFilterSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyTrafficMirrorFilterNetworkServices(
	w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring,
) {
	out, err := t.ModifyTrafficMirrorFilterNetworkServices(r.Context(),
		r.Form.Get("TrafficMirrorFilterId"),
		awsquery.ListStrings(r.Form, "AddNetworkService"),
		awsquery.ListStrings(r.Form, "RemoveNetworkService"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"ModifyTrafficMirrorFilterNetworkServicesResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Filter  trafficMirrorFilterXML `xml:"trafficMirrorFilter"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Filter: toTrafficMirrorFilterXML(out)})
}

func (*Handler) createTrafficMirrorFilterRule(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	out, err := t.CreateTrafficMirrorFilterRule(r.Context(), trafficMirrorRuleConfig(r))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                   `xml:"CreateTrafficMirrorFilterRuleResponse"`
		Xmlns   string                     `xml:"xmlns,attr"`
		Req     string                     `xml:"requestId"`
		Rule    trafficMirrorFilterRuleXML `xml:"trafficMirrorFilterRule"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Rule: toFilterRuleXML(out)})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) modifyTrafficMirrorFilterRule(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	out, err := t.ModifyTrafficMirrorFilterRule(r.Context(),
		r.Form.Get("TrafficMirrorFilterRuleId"),
		trafficMirrorRuleConfig(r),
		awsquery.ListStrings(r.Form, "RemoveField"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                   `xml:"ModifyTrafficMirrorFilterRuleResponse"`
		Xmlns   string                     `xml:"xmlns,attr"`
		Req     string                     `xml:"requestId"`
		Rule    trafficMirrorFilterRuleXML `xml:"trafficMirrorFilterRule"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Rule: toFilterRuleXML(out)})
}

func (*Handler) deleteTrafficMirrorFilterRule(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	id := r.Form.Get("TrafficMirrorFilterRuleId")
	if err := t.DeleteTrafficMirrorFilterRule(r.Context(), id); err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteTrafficMirrorFilterRuleResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"trafficMirrorFilterRuleId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

func (*Handler) describeTrafficMirrorFilterRules(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	items, err := t.DescribeTrafficMirrorFilterRules(r.Context(),
		r.Form.Get("TrafficMirrorFilterId"),
		awsquery.ListStrings(r.Form, "TrafficMirrorFilterRuleId"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	out := make([]trafficMirrorFilterRuleXML, 0, len(items))
	for i := range items {
		out = append(out, toFilterRuleXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                     `xml:"DescribeTrafficMirrorFilterRulesResponse"`
		Xmlns   string                       `xml:"xmlns,attr"`
		Req     string                       `xml:"requestId"`
		Set     []trafficMirrorFilterRuleXML `xml:"trafficMirrorFilterRuleSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

// ---- Session handlers ----

func (*Handler) createTrafficMirrorSession(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	out, err := t.CreateTrafficMirrorSession(r.Context(), trafficMirrorSessionConfig(r))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                `xml:"CreateTrafficMirrorSessionResponse"`
		Xmlns   string                  `xml:"xmlns,attr"`
		Req     string                  `xml:"requestId"`
		Session trafficMirrorSessionXML `xml:"trafficMirrorSession"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Session: toTrafficMirrorSessionXML(out)})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) modifyTrafficMirrorSession(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	out, err := t.ModifyTrafficMirrorSession(r.Context(),
		r.Form.Get("TrafficMirrorSessionId"),
		trafficMirrorSessionConfig(r),
		awsquery.ListStrings(r.Form, "RemoveField"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                `xml:"ModifyTrafficMirrorSessionResponse"`
		Xmlns   string                  `xml:"xmlns,attr"`
		Req     string                  `xml:"requestId"`
		Session trafficMirrorSessionXML `xml:"trafficMirrorSession"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Session: toTrafficMirrorSessionXML(out)})
}

func (*Handler) deleteTrafficMirrorSession(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	id := r.Form.Get("TrafficMirrorSessionId")
	if err := t.DeleteTrafficMirrorSession(r.Context(), id); err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteTrafficMirrorSessionResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"trafficMirrorSessionId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeTrafficMirrorSessions(w http.ResponseWriter, r *http.Request, t netdriver.TrafficMirroring) {
	items, err := t.DescribeTrafficMirrorSessions(r.Context(), awsquery.ListStrings(r.Form, "TrafficMirrorSessionId"))
	if err != nil {
		writeTrafficMirrorErr(w, err)
		return
	}

	out := make([]trafficMirrorSessionXML, 0, len(items))
	for i := range items {
		out = append(out, toTrafficMirrorSessionXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                  `xml:"DescribeTrafficMirrorSessionsResponse"`
		Xmlns   string                    `xml:"xmlns,attr"`
		Req     string                    `xml:"requestId"`
		Set     []trafficMirrorSessionXML `xml:"trafficMirrorSessionSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

// ---- request parsing ----

func trafficMirrorRuleConfig(r *http.Request) netdriver.TrafficMirrorFilterRuleConfig {
	return netdriver.TrafficMirrorFilterRuleConfig{
		FilterID:             r.Form.Get("TrafficMirrorFilterId"),
		TrafficDirection:     r.Form.Get("TrafficDirection"),
		RuleNumber:           formInt32(r, "RuleNumber"),
		RuleAction:           r.Form.Get("RuleAction"),
		Protocol:             formInt32(r, "Protocol"),
		DestinationCIDR:      r.Form.Get("DestinationCidrBlock"),
		SourceCIDR:           r.Form.Get("SourceCidrBlock"),
		DestinationPortRange: formPortRange(r, "DestinationPortRange"),
		SourcePortRange:      formPortRange(r, "SourcePortRange"),
		Description:          r.Form.Get("Description"),
	}
}

func trafficMirrorSessionConfig(r *http.Request) netdriver.TrafficMirrorSessionConfig {
	return netdriver.TrafficMirrorSessionConfig{
		NetworkInterfaceID:    r.Form.Get("NetworkInterfaceId"),
		TrafficMirrorTargetID: r.Form.Get("TrafficMirrorTargetId"),
		TrafficMirrorFilterID: r.Form.Get("TrafficMirrorFilterId"),
		PacketLength:          formInt32(r, "PacketLength"),
		SessionNumber:         formInt32(r, "SessionNumber"),
		VirtualNetworkID:      formInt32(r, "VirtualNetworkId"),
		Description:           r.Form.Get("Description"),
		Tags:                  mergeTagSpecs(awsquery.TagSpecs(r.Form), "traffic-mirror-session"),
	}
}

func formInt32(r *http.Request, key string) int32 {
	v, err := strconv.ParseInt(r.Form.Get(key), 10, 32)
	if err != nil {
		return 0
	}

	return int32(v)
}

func formPortRange(r *http.Request, prefix string) *netdriver.TrafficMirrorPortRange {
	from := r.Form.Get(prefix + ".FromPort")
	to := r.Form.Get(prefix + ".ToPort")

	if from == "" && to == "" {
		return nil
	}

	return &netdriver.TrafficMirrorPortRange{
		FromPort: formInt32(r, prefix+".FromPort"),
		ToPort:   formInt32(r, prefix+".ToPort"),
	}
}

// ---- driver → XML ----

func toTrafficMirrorTargetXML(t *netdriver.TrafficMirrorTarget) trafficMirrorTargetXML {
	return trafficMirrorTargetXML{
		TrafficMirrorTargetID:         t.ID,
		NetworkInterfaceID:            t.NetworkInterfaceID,
		NetworkLoadBalancerArn:        t.NetworkLoadBalancerARN,
		GatewayLoadBalancerEndpointID: t.GatewayLoadBalancerEndpointID,
		Type:                          t.Type,
		Description:                   t.Description,
		OwnerID:                       t.OwnerID,
		Tags:                          toTagItems(t.Tags),
	}
}

func toTrafficMirrorFilterXML(f *netdriver.TrafficMirrorFilter) trafficMirrorFilterXML {
	return trafficMirrorFilterXML{
		TrafficMirrorFilterID: f.ID,
		Description:           f.Description,
		IngressFilterRules:    toFilterRuleXMLs(f.IngressRules),
		EgressFilterRules:     toFilterRuleXMLs(f.EgressRules),
		NetworkServices:       f.NetworkServices,
		Tags:                  toTagItems(f.Tags),
	}
}

func toFilterRuleXMLs(rules []netdriver.TrafficMirrorFilterRule) []trafficMirrorFilterRuleXML {
	if len(rules) == 0 {
		return nil
	}

	out := make([]trafficMirrorFilterRuleXML, 0, len(rules))
	for i := range rules {
		out = append(out, toFilterRuleXML(&rules[i]))
	}

	return out
}

func toFilterRuleXML(r *netdriver.TrafficMirrorFilterRule) trafficMirrorFilterRuleXML {
	return trafficMirrorFilterRuleXML{
		TrafficMirrorFilterRuleID: r.ID,
		TrafficMirrorFilterID:     r.FilterID,
		TrafficDirection:          r.TrafficDirection,
		RuleNumber:                r.RuleNumber,
		RuleAction:                r.RuleAction,
		Protocol:                  r.Protocol,
		DestinationCidrBlock:      r.DestinationCIDR,
		SourceCidrBlock:           r.SourceCIDR,
		DestinationPortRange:      toPortRangeXML(r.DestinationPortRange),
		SourcePortRange:           toPortRangeXML(r.SourcePortRange),
		Description:               r.Description,
	}
}

func toPortRangeXML(p *netdriver.TrafficMirrorPortRange) *trafficMirrorPortRangeXML {
	if p == nil {
		return nil
	}

	return &trafficMirrorPortRangeXML{FromPort: p.FromPort, ToPort: p.ToPort}
}

func toTrafficMirrorSessionXML(s *netdriver.TrafficMirrorSession) trafficMirrorSessionXML {
	return trafficMirrorSessionXML{
		TrafficMirrorSessionID: s.ID,
		TrafficMirrorTargetID:  s.TrafficMirrorTargetID,
		TrafficMirrorFilterID:  s.TrafficMirrorFilterID,
		NetworkInterfaceID:     s.NetworkInterfaceID,
		PacketLength:           s.PacketLength,
		SessionNumber:          s.SessionNumber,
		VirtualNetworkID:       s.VirtualNetworkID,
		Description:            s.Description,
		OwnerID:                s.OwnerID,
		Tags:                   toTagItems(s.Tags),
	}
}

func writeTrafficMirrorErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidTrafficMirrorTargetId.NotFound", "DependencyViolation")
}
