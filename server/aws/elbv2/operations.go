package elbv2

import (
	"net/http"
	"net/url"
	"strconv"

	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// --- load balancers ---

func (h *Handler) createLoadBalancer(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := lbdriver.LBConfig{
		Name:    form.Get("Name"),
		Type:    typeOrDefault(form.Get("Type")),
		Scheme:  schemeOrDefault(form.Get("Scheme")),
		Subnets: awsquery.ListStrings(form, "Subnets.member"),
		Tags:    parseTags(form),
	}

	lb, err := h.lb.CreateLoadBalancer(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createLoadBalancerResponse{
		Xmlns:    Namespace,
		Result:   loadBalancersResult{LoadBalancers: loadBalancersXML{Member: []loadBalancerXML{toLoadBalancerXML(lb)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeLoadBalancers(w http.ResponseWriter, r *http.Request) {
	arns, err := h.resolveLBArns(r)
	if err != nil {
		writeErr(w, err)
		return
	}

	lbs, err := h.lb.DescribeLoadBalancers(r.Context(), arns)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := loadBalancersXML{Member: make([]loadBalancerXML, 0, len(lbs))}
	for i := range lbs {
		out.Member = append(out.Member, toLoadBalancerXML(&lbs[i]))
	}

	awsquery.WriteXMLResponse(w, describeLoadBalancersResponse{
		Xmlns:    Namespace,
		Result:   loadBalancersResult{LoadBalancers: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// resolveLBArns turns the DescribeLoadBalancers filter parameters (arns and/or
// names) into a list of driver ARNs. An empty result means "all".
func (h *Handler) resolveLBArns(r *http.Request) ([]string, error) {
	arns := awsquery.ListStrings(r.Form, "LoadBalancerArns.member")

	names := awsquery.ListStrings(r.Form, "Names.member")
	if len(names) == 0 {
		return arns, nil
	}

	all, err := h.lb.DescribeLoadBalancers(r.Context(), nil)
	if err != nil {
		return nil, err
	}

	for _, name := range names {
		for i := range all {
			if all[i].Name == name {
				arns = append(arns, all[i].ARN)
			}
		}
	}

	return arns, nil
}

func (h *Handler) deleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("LoadBalancerArn")

	if err := h.lb.DeleteLoadBalancer(r.Context(), arn); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteLoadBalancerResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- target groups ---

func (h *Handler) createTargetGroup(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := lbdriver.TargetGroupConfig{
		Name:       form.Get("Name"),
		Protocol:   form.Get("Protocol"),
		Port:       formInt(form.Get("Port")),
		VPCID:      form.Get("VpcId"),
		HealthPath: form.Get("HealthCheckPath"),
		Tags:       parseTags(form),
	}

	tg, err := h.lb.CreateTargetGroup(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createTargetGroupResponse{
		Xmlns:    Namespace,
		Result:   targetGroupsResult{TargetGroups: targetGroupsXML{Member: []targetGroupXML{toTargetGroupXML(tg)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeTargetGroups(w http.ResponseWriter, r *http.Request) {
	arns, err := h.resolveTGArns(r)
	if err != nil {
		writeErr(w, err)
		return
	}

	tgs, err := h.lb.DescribeTargetGroups(r.Context(), arns)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := targetGroupsXML{Member: make([]targetGroupXML, 0, len(tgs))}
	for i := range tgs {
		out.Member = append(out.Member, toTargetGroupXML(&tgs[i]))
	}

	awsquery.WriteXMLResponse(w, describeTargetGroupsResponse{
		Xmlns:    Namespace,
		Result:   targetGroupsResult{TargetGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// resolveTGArns turns the DescribeTargetGroups filter parameters (arns and/or
// names) into a list of driver ARNs. An empty result means "all".
func (h *Handler) resolveTGArns(r *http.Request) ([]string, error) {
	arns := awsquery.ListStrings(r.Form, "TargetGroupArns.member")

	names := awsquery.ListStrings(r.Form, "Names.member")
	if len(names) == 0 {
		return arns, nil
	}

	all, err := h.lb.DescribeTargetGroups(r.Context(), nil)
	if err != nil {
		return nil, err
	}

	for _, name := range names {
		for i := range all {
			if all[i].Name == name {
				arns = append(arns, all[i].ARN)
			}
		}
	}

	return arns, nil
}

func (h *Handler) deleteTargetGroup(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("TargetGroupArn")

	if err := h.lb.DeleteTargetGroup(r.Context(), arn); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteTargetGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- listeners ---

func (h *Handler) createListener(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := lbdriver.ListenerConfig{
		LBARN:          form.Get("LoadBalancerArn"),
		Protocol:       form.Get("Protocol"),
		Port:           formInt(form.Get("Port")),
		TargetGroupARN: firstForwardTargetGroup(form, "DefaultActions.member"),
	}

	li, err := h.lb.CreateListener(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createListenerResponse{
		Xmlns:    Namespace,
		Result:   listenersResult{Listeners: listenersXML{Member: []listenerXML{toListenerXML(li)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeListeners(w http.ResponseWriter, r *http.Request) {
	lbARN := r.Form.Get("LoadBalancerArn")

	lis, err := h.lb.DescribeListeners(r.Context(), lbARN)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Filter to specific listener ARNs if requested.
	if wanted := awsquery.ListStrings(r.Form, "ListenerArns.member"); len(wanted) > 0 {
		lis = filterListeners(lis, wanted)
	}

	out := listenersXML{Member: make([]listenerXML, 0, len(lis))}
	for i := range lis {
		out.Member = append(out.Member, toListenerXML(&lis[i]))
	}

	awsquery.WriteXMLResponse(w, describeListenersResponse{
		Xmlns:    Namespace,
		Result:   listenersResult{Listeners: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteListener(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("ListenerArn")

	if err := h.lb.DeleteListener(r.Context(), arn); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteListenerResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- rules ---

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := lbdriver.RuleConfig{
		ListenerARN: form.Get("ListenerArn"),
		Priority:    formInt(form.Get("Priority")),
		Conditions:  parseConditions(form, "Conditions.member"),
		Actions:     parseActions(form, "Actions.member"),
	}

	rule, err := h.lb.CreateRule(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createRuleResponse{
		Xmlns:    Namespace,
		Result:   rulesResult{Rules: rulesXML{Member: []ruleXML{toRuleXML(rule)}}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeRules(w http.ResponseWriter, r *http.Request) {
	listenerARN := r.Form.Get("ListenerArn")

	rules, err := h.lb.DescribeRules(r.Context(), listenerARN)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := rulesXML{Member: make([]ruleXML, 0, len(rules))}
	for i := range rules {
		out.Member = append(out.Member, toRuleXML(&rules[i]))
	}

	awsquery.WriteXMLResponse(w, describeRulesResponse{
		Xmlns:    Namespace,
		Result:   rulesResult{Rules: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("RuleArn")

	if err := h.lb.DeleteRule(r.Context(), arn); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRuleResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- targets / health ---

func (h *Handler) registerTargets(w http.ResponseWriter, r *http.Request) {
	tgARN := r.Form.Get("TargetGroupArn")
	targets := parseTargets(r.Form, "Targets.member")

	if err := h.lb.RegisterTargets(r.Context(), tgARN, targets); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, registerTargetsResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deregisterTargets(w http.ResponseWriter, r *http.Request) {
	tgARN := r.Form.Get("TargetGroupArn")
	targets := parseTargets(r.Form, "Targets.member")

	if err := h.lb.DeregisterTargets(r.Context(), tgARN, targets); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deregisterTargetsResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeTargetHealth(w http.ResponseWriter, r *http.Request) {
	tgARN := r.Form.Get("TargetGroupArn")

	health, err := h.lb.DescribeTargetHealth(r.Context(), tgARN)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Optional filter: only the requested targets.
	if wanted := parseTargets(r.Form, "Targets.member"); len(wanted) > 0 {
		health = filterHealth(health, wanted)
	}

	out := targetHealthDescriptionsXML{Member: make([]targetHealthDescriptionXML, 0, len(health))}
	for i := range health {
		th := health[i]
		out.Member = append(out.Member, targetHealthDescriptionXML{
			Target: targetDescriptionXML{ID: th.Target.ID, Port: th.Target.Port},
			TargetHealth: &targetHealthXML{
				State:  th.State,
				Reason: th.Reason,
			},
		})
	}

	awsquery.WriteXMLResponse(w, describeTargetHealthResponse{
		Xmlns:    Namespace,
		Result:   describeTargetHealthResult{TargetHealthDescriptions: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- form parsing helpers ---

// parseTags parses the ELBv2 Tags.member.N.{Key,Value} entries.
func parseTags(form url.Values) map[string]string {
	indices := awsquery.CollectIndices(form, "Tags.member")
	if len(indices) == 0 {
		return nil
	}

	out := make(map[string]string, len(indices))

	for _, n := range indices {
		base := "Tags.member." + strconv.Itoa(n)
		if k := form.Get(base + ".Key"); k != "" {
			out[k] = form.Get(base + ".Value")
		}
	}

	return out
}

// firstForwardTargetGroup returns the TargetGroupArn of the first forward
// action in the given action-list prefix, checking both the flat form
// (member.N.TargetGroupArn) and the ForwardConfig shape.
func firstForwardTargetGroup(form url.Values, prefix string) string {
	for _, a := range parseActions(form, prefix) {
		if a.TargetGroupARN != "" {
			return a.TargetGroupARN
		}
	}

	return ""
}

// parseActions parses an Actions/DefaultActions member list into driver
// RuleActions. Both the flat TargetGroupArn field and the
// ForwardConfig.TargetGroups.member.N.TargetGroupArn nesting are accepted.
func parseActions(form url.Values, prefix string) []lbdriver.RuleAction {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.RuleAction, 0, len(indices))

	for _, n := range indices {
		base := prefix + "." + strconv.Itoa(n)

		tgARN := form.Get(base + ".TargetGroupArn")
		if tgARN == "" {
			tgARN = form.Get(base + ".ForwardConfig.TargetGroups.member.1.TargetGroupArn")
		}

		out = append(out, lbdriver.RuleAction{
			Type:           typeOr(form.Get(base+".Type"), "forward"),
			TargetGroupARN: tgARN,
		})
	}

	return out
}

// parseConditions parses a Conditions member list into driver RuleConditions.
func parseConditions(form url.Values, prefix string) []lbdriver.RuleCondition {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.RuleCondition, 0, len(indices))

	for _, n := range indices {
		base := prefix + "." + strconv.Itoa(n)

		field := form.Get(base + ".Field")

		values := awsquery.ListStrings(form, base+".Values.member")
		if len(values) == 0 {
			// Some SDKs nest values under the typed config
			// (PathPatternConfig / HostHeaderConfig).
			values = append(values,
				awsquery.ListStrings(form, base+".PathPatternConfig.Values.member")...)
			values = append(values,
				awsquery.ListStrings(form, base+".HostHeaderConfig.Values.member")...)
		}

		out = append(out, lbdriver.RuleCondition{Field: field, Values: values})
	}

	return out
}

// parseTargets parses a Targets member list into driver Targets.
func parseTargets(form url.Values, prefix string) []lbdriver.Target {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.Target, 0, len(indices))

	for _, n := range indices {
		base := prefix + "." + strconv.Itoa(n)

		id := form.Get(base + ".Id")
		if id == "" {
			continue
		}

		out = append(out, lbdriver.Target{ID: id, Port: formInt(form.Get(base + ".Port"))})
	}

	return out
}

// filterListeners keeps only listeners whose ARN is in wanted.
func filterListeners(lis []lbdriver.ListenerInfo, wanted []string) []lbdriver.ListenerInfo {
	set := make(map[string]struct{}, len(wanted))
	for _, w := range wanted {
		set[w] = struct{}{}
	}

	out := lis[:0]
	for i := range lis {
		if _, ok := set[lis[i].ARN]; ok {
			out = append(out, lis[i])
		}
	}

	return out
}

// filterHealth keeps only the health entries whose target ID appears in wanted.
func filterHealth(health []lbdriver.TargetHealth, wanted []lbdriver.Target) []lbdriver.TargetHealth {
	set := make(map[string]struct{}, len(wanted))
	for _, t := range wanted {
		set[t.ID] = struct{}{}
	}

	out := health[:0]
	for i := range health {
		if _, ok := set[health[i].Target.ID]; ok {
			out = append(out, health[i])
		}
	}

	return out
}

// toRuleXML converts a driver RuleInfo to its XML representation.
func toRuleXML(rule *lbdriver.RuleInfo) ruleXML {
	out := ruleXML{
		RuleArn:   rule.ARN,
		Priority:  priorityString(rule.Priority, rule.IsDefault),
		IsDefault: rule.IsDefault,
	}

	if len(rule.Conditions) > 0 {
		conds := &ruleConditionsXML{}
		for _, c := range rule.Conditions {
			conds.Member = append(conds.Member, ruleConditionXML{
				Field:  c.Field,
				Values: &stringListXML{Member: c.Values},
			})
		}

		out.Conditions = conds
	}

	if len(rule.Actions) > 0 {
		acts := &actionsXML{}
		for _, a := range rule.Actions {
			acts.Member = append(acts.Member, actionXML{
				Type:           a.Type,
				TargetGroupArn: a.TargetGroupARN,
			})
		}

		out.Actions = acts
	}

	return out
}

// priorityString renders a rule priority for the wire. Default rules serialize
// as "default"; others as their numeric priority.
func priorityString(priority int, isDefault bool) string {
	if isDefault {
		return "default"
	}

	return strconv.Itoa(priority)
}

// typeOrDefault maps an empty load-balancer type to ELBv2's default
// ("application").
func typeOrDefault(t string) string {
	if t == "" {
		return "application"
	}

	return t
}

// schemeOrDefault maps an empty scheme to ELBv2's default ("internet-facing").
func schemeOrDefault(s string) string {
	if s == "" {
		return "internet-facing"
	}

	return s
}

func typeOr(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// formInt returns the integer value of a form field, or 0 on missing/parse error.
func formInt(v string) int {
	if v == "" {
		return 0
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}

	return n
}
