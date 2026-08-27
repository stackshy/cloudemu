package elbv2

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// --- load balancers ---

func (h *Handler) createLoadBalancer(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := lbdriver.LBConfig{
		Name:           form.Get("Name"),
		Type:           typeOrDefault(form.Get("Type")),
		Scheme:         schemeOrDefault(form.Get("Scheme")),
		Subnets:        awsquery.ListStrings(form, "Subnets.member"),
		SecurityGroups: awsquery.ListStrings(form, "SecurityGroups.member"),
		IPAddressType:  form.Get("IpAddressType"),
		Tags:           parseTags(form),
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

	// Sort for a stable order so the offset-based Marker is meaningful.
	sort.Slice(lbs, func(i, j int) bool { return lbs[i].ARN < lbs[j].ARN })

	start, end, next, err := pageWindow(r.Form.Get("Marker"), formInt(r.Form.Get("PageSize")), len(lbs))
	if err != nil {
		writeErr(w, err)
		return
	}

	lbs = lbs[start:end]

	out := loadBalancersXML{Member: make([]loadBalancerXML, 0, len(lbs))}
	for i := range lbs {
		out.Member = append(out.Member, toLoadBalancerXML(&lbs[i]))
	}

	awsquery.WriteXMLResponse(w, describeLoadBalancersResponse{
		Xmlns:    Namespace,
		Result:   loadBalancersResult{LoadBalancers: out, NextMarker: next},
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

	// A Names filter that resolves to nothing must not fall through to the
	// driver's "empty means all" behavior — real ELBv2 returns
	// LoadBalancerNotFound for a name that doesn't exist.
	if len(arns) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", names[0])
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
		Name:        form.Get("Name"),
		Protocol:    form.Get("Protocol"),
		Port:        formInt(form.Get("Port")),
		VPCID:       form.Get("VpcId"),
		TargetType:  form.Get("TargetType"),
		HealthPath:  form.Get("HealthCheckPath"),
		HealthCheck: parseHealthCheck(form),
		Tags:        parseTags(form),
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

	sort.Slice(tgs, func(i, j int) bool { return tgs[i].ARN < tgs[j].ARN })

	start, end, next, err := pageWindow(r.Form.Get("Marker"), formInt(r.Form.Get("PageSize")), len(tgs))
	if err != nil {
		writeErr(w, err)
		return
	}

	tgs = tgs[start:end]

	out := targetGroupsXML{Member: make([]targetGroupXML, 0, len(tgs))}
	for i := range tgs {
		out.Member = append(out.Member, toTargetGroupXML(&tgs[i]))
	}

	awsquery.WriteXMLResponse(w, describeTargetGroupsResponse{
		Xmlns:    Namespace,
		Result:   targetGroupsResult{TargetGroups: out, NextMarker: next},
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

	// As in resolveLBArns: a Names filter matching nothing is TargetGroupNotFound,
	// not "return all".
	if len(arns) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "target group %q not found", names[0])
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
		DefaultActions: parseActions(form, "DefaultActions.member"),
		SslPolicy:      form.Get("SslPolicy"),
		Certificates:   parseCertificates(form, "Certificates.member"),
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
	wanted := awsquery.ListStrings(r.Form, "ListenerArns.member")

	// LoadBalancerArn and ListenerArns are alternative, both-optional filters.
	// A DescribeListeners with only ListenerArns must resolve those listeners by
	// ARN, not fall through to a load-balancer existence check on an empty ARN.
	var (
		lis []lbdriver.ListenerInfo
		err error
	)

	if lbARN == "" && len(wanted) > 0 {
		lis, err = h.listenersByARN(r.Context(), wanted)
	} else {
		lis, err = h.lb.DescribeListeners(r.Context(), lbARN)
		if err == nil && len(wanted) > 0 {
			lis = filterListeners(lis, wanted)
		}
	}

	if err != nil {
		writeErr(w, err)
		return
	}

	// Sort for a stable order so the offset-based Marker is meaningful.
	sort.Slice(lis, func(i, j int) bool { return lis[i].ARN < lis[j].ARN })

	start, end, next, err := pageWindow(r.Form.Get("Marker"), formInt(r.Form.Get("PageSize")), len(lis))
	if err != nil {
		writeErr(w, err)
		return
	}

	lis = lis[start:end]

	out := listenersXML{Member: make([]listenerXML, 0, len(lis))}
	for i := range lis {
		out.Member = append(out.Member, toListenerXML(&lis[i]))
	}

	awsquery.WriteXMLResponse(w, describeListenersResponse{
		Xmlns:    Namespace,
		Result:   listenersResult{Listeners: out, NextMarker: next},
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

	// Sort for a stable order so the offset-based Marker is meaningful.
	sort.Slice(rules, func(i, j int) bool { return rules[i].ARN < rules[j].ARN })

	start, end, next, err := pageWindow(r.Form.Get("Marker"), formInt(r.Form.Get("PageSize")), len(rules))
	if err != nil {
		writeErr(w, err)
		return
	}

	rules = rules[start:end]

	out := rulesXML{Member: make([]ruleXML, 0, len(rules))}
	for i := range rules {
		out.Member = append(out.Member, toRuleXML(&rules[i]))
	}

	awsquery.WriteXMLResponse(w, describeRulesResponse{
		Xmlns:    Namespace,
		Result:   rulesResult{Rules: out, NextMarker: next},
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
				State:       th.State,
				Reason:      th.Reason,
				Description: th.Description,
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

// parseHealthCheck parses the health-check fields of a CreateTargetGroup
// request. Unset fields stay zero so the driver can apply its protocol-derived
// defaults.
func parseHealthCheck(form url.Values) lbdriver.HealthCheck {
	return lbdriver.HealthCheck{
		Protocol:           form.Get("HealthCheckProtocol"),
		Port:               form.Get("HealthCheckPort"),
		Path:               form.Get("HealthCheckPath"),
		IntervalSeconds:    formInt(form.Get("HealthCheckIntervalSeconds")),
		TimeoutSeconds:     formInt(form.Get("HealthCheckTimeoutSeconds")),
		HealthyThreshold:   formInt(form.Get("HealthyThresholdCount")),
		UnhealthyThreshold: formInt(form.Get("UnhealthyThresholdCount")),
		Matcher:            form.Get("Matcher.HttpCode"),
	}
}

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

// parseActions parses an Actions/DefaultActions member list into driver
// RuleActions, preserving the full shape of forward, redirect and
// fixed-response actions so they round-trip on Describe.
func parseActions(form url.Values, prefix string) []lbdriver.RuleAction {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.RuleAction, 0, len(indices))
	for _, n := range indices {
		out = append(out, parseAction(form, prefix+"."+strconv.Itoa(n)))
	}

	return out
}

// parseAction parses a single action at the given form prefix. Both the flat
// TargetGroupArn field and the ForwardConfig.TargetGroups.member.N nesting are
// accepted for forward actions; a multi-target-group (weighted) ForwardConfig is
// preserved in full so canary / blue-green splits round-trip on Describe.
func parseAction(form url.Values, base string) lbdriver.RuleAction {
	forward := parseForwardConfig(form, base+".ForwardConfig")

	tgARN := form.Get(base + ".TargetGroupArn")
	if tgARN == "" && len(forward) > 0 {
		tgARN = forward[0].TargetGroupARN
	}

	return lbdriver.RuleAction{
		Type:                typeOr(form.Get(base+".Type"), "forward"),
		TargetGroupARN:      tgARN,
		ForwardConfig:       forward,
		Order:               formInt(form.Get(base + ".Order")),
		RedirectConfig:      parseRedirectConfig(form, base+".RedirectConfig"),
		FixedResponseConfig: parseFixedResponseConfig(form, base+".FixedResponseConfig"),
	}
}

// parseForwardConfig parses a forward action's ForwardConfig.TargetGroups member
// list into weighted target groups, returning nil when none are present. A
// single-target forward carried only as the flat TargetGroupArn yields nil here;
// the caller keeps the scalar field for that case.
func parseForwardConfig(form url.Values, base string) []lbdriver.ForwardTargetGroup {
	indices := awsquery.CollectIndices(form, base+".TargetGroups.member")
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.ForwardTargetGroup, 0, len(indices))

	for _, n := range indices {
		member := base + ".TargetGroups.member." + strconv.Itoa(n)

		arn := form.Get(member + ".TargetGroupArn")
		if arn == "" {
			continue
		}

		out = append(out, lbdriver.ForwardTargetGroup{
			TargetGroupARN: arn,
			Weight:         formInt32(form.Get(member + ".Weight")),
		})
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// parseRedirectConfig parses a RedirectConfig sub-structure, returning nil when
// none of its fields are present.
func parseRedirectConfig(form url.Values, base string) *lbdriver.RedirectActionConfig {
	rc := lbdriver.RedirectActionConfig{
		Protocol:   form.Get(base + ".Protocol"),
		Port:       form.Get(base + ".Port"),
		Host:       form.Get(base + ".Host"),
		Path:       form.Get(base + ".Path"),
		Query:      form.Get(base + ".Query"),
		StatusCode: form.Get(base + ".StatusCode"),
	}

	if rc == (lbdriver.RedirectActionConfig{}) {
		return nil
	}

	return &rc
}

// parseFixedResponseConfig parses a FixedResponseConfig sub-structure, returning
// nil when none of its fields are present.
func parseFixedResponseConfig(form url.Values, base string) *lbdriver.FixedResponseActionConfig {
	fr := lbdriver.FixedResponseActionConfig{
		StatusCode:  form.Get(base + ".StatusCode"),
		ContentType: form.Get(base + ".ContentType"),
		MessageBody: form.Get(base + ".MessageBody"),
	}

	if fr == (lbdriver.FixedResponseActionConfig{}) {
		return nil
	}

	return &fr
}

// parseCertificates parses a listener Certificates member list. The listener's
// create certificate is its default, so the first entry is marked IsDefault —
// matching what real ELBv2 reports on DescribeListeners.
func parseCertificates(form url.Values, prefix string) []lbdriver.Certificate {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.Certificate, 0, len(indices))

	for i, n := range indices {
		arn := form.Get(prefix + "." + strconv.Itoa(n) + ".CertificateArn")
		if arn == "" {
			continue
		}

		out = append(out, lbdriver.Certificate{CertificateArn: arn, IsDefault: i == 0})
	}

	return out
}

// parseConditions parses a Conditions member list into driver RuleConditions,
// preserving each condition's typed config so it round-trips on DescribeRules.
func parseConditions(form url.Values, prefix string) []lbdriver.RuleCondition {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]lbdriver.RuleCondition, 0, len(indices))
	for _, n := range indices {
		out = append(out, parseCondition(form, prefix+"."+strconv.Itoa(n)))
	}

	return out
}

// parseCondition parses a single rule condition at the given form prefix.
func parseCondition(form url.Values, base string) lbdriver.RuleCondition {
	c := lbdriver.RuleCondition{
		Field:  form.Get(base + ".Field"),
		Values: awsquery.ListStrings(form, base+".Values.member"),
	}

	if v := awsquery.ListStrings(form, base+".HostHeaderConfig.Values.member"); len(v) > 0 {
		c.HostHeaderConfig = &lbdriver.HostHeaderConditionConfig{Values: v}
	}

	if v := awsquery.ListStrings(form, base+".PathPatternConfig.Values.member"); len(v) > 0 {
		c.PathPatternConfig = &lbdriver.PathPatternConditionConfig{Values: v}
	}

	c.HTTPHeaderConfig = parseHTTPHeaderConfig(form, base+".HttpHeaderConfig")
	c.QueryStringConfig = parseQueryStringConfig(form, base+".QueryStringConfig")

	if v := awsquery.ListStrings(form, base+".SourceIpConfig.Values.member"); len(v) > 0 {
		c.SourceIPConfig = &lbdriver.SourceIPConditionConfig{Values: v}
	}

	if v := awsquery.ListStrings(form, base+".HttpRequestMethodConfig.Values.member"); len(v) > 0 {
		c.HTTPRequestMethodConfig = &lbdriver.HTTPRequestMethodConditionConfig{Values: v}
	}

	// AWS still echoes the deprecated flat Values for path-pattern/host-header,
	// so backfill it from the typed config when the caller only sent the config.
	if len(c.Values) == 0 {
		switch {
		case c.PathPatternConfig != nil:
			c.Values = c.PathPatternConfig.Values
		case c.HostHeaderConfig != nil:
			c.Values = c.HostHeaderConfig.Values
		}
	}

	return c
}

// parseHTTPHeaderConfig parses an HttpHeaderConfig sub-structure, returning nil
// when absent.
func parseHTTPHeaderConfig(form url.Values, base string) *lbdriver.HTTPHeaderConditionConfig {
	name := form.Get(base + ".HttpHeaderName")
	values := awsquery.ListStrings(form, base+".Values.member")

	if name == "" && len(values) == 0 {
		return nil
	}

	return &lbdriver.HTTPHeaderConditionConfig{HTTPHeaderName: name, Values: values}
}

// parseQueryStringConfig parses a QueryStringConfig sub-structure (a list of
// key/value pairs), returning nil when absent.
func parseQueryStringConfig(form url.Values, base string) *lbdriver.QueryStringConditionConfig {
	indices := awsquery.CollectIndices(form, base+".Values.member")
	if len(indices) == 0 {
		return nil
	}

	pairs := make([]lbdriver.QueryStringKeyValue, 0, len(indices))

	for _, n := range indices {
		p := base + ".Values.member." + strconv.Itoa(n)
		pairs = append(pairs, lbdriver.QueryStringKeyValue{
			Key:   form.Get(p + ".Key"),
			Value: form.Get(p + ".Value"),
		})
	}

	return &lbdriver.QueryStringConditionConfig{Values: pairs}
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

// listenersByARN resolves the named listeners directly by ARN (used when
// DescribeListeners is called with ListenerArns and no LoadBalancerArn). A
// listener ARN that does not exist yields ListenerNotFound.
func (h *Handler) listenersByARN(ctx context.Context, arns []string) ([]lbdriver.ListenerInfo, error) {
	getter, ok := h.lb.(lbdriver.ListenerGetter)
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, "listener lookup by ARN is not supported")
	}

	out := make([]lbdriver.ListenerInfo, 0, len(arns))

	for _, arn := range arns {
		li, err := getter.GetListener(ctx, arn)
		if err != nil {
			return nil, err
		}

		out = append(out, *li)
	}

	return out, nil
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

// healthKey identifies a registered target by (ID, Port). AWS lets the same
// instance ID be registered on multiple ports, so a bare ID is not unique.
type healthKey struct {
	id   string
	port int
}

// filterHealth resolves the health for the explicitly requested targets. Real
// ELBv2 does not silently drop a requested target that isn't registered: it
// returns a TargetHealthDescription with State=unused and
// Reason=Target.NotRegistered. Registered targets keep their real health. A
// requested target with no port matches every registration of that instance ID.
func filterHealth(health []lbdriver.TargetHealth, wanted []lbdriver.Target) []lbdriver.TargetHealth {
	byKey := make(map[healthKey]lbdriver.TargetHealth, len(health))
	byID := make(map[string][]lbdriver.TargetHealth, len(health))

	for i := range health {
		t := health[i].Target
		byKey[healthKey{t.ID, t.Port}] = health[i]
		byID[t.ID] = append(byID[t.ID], health[i])
	}

	out := make([]lbdriver.TargetHealth, 0, len(wanted))

	for _, w := range wanted {
		if w.Port != 0 {
			if h, ok := byKey[healthKey{w.ID, w.Port}]; ok {
				out = append(out, h)
				continue
			}
		} else if hs, ok := byID[w.ID]; ok {
			out = append(out, hs...)
			continue
		}

		out = append(out, lbdriver.TargetHealth{
			Target:      w,
			State:       "unused",
			Reason:      "Target.NotRegistered",
			Description: "Target is not registered to the target group",
		})
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
		for i := range rule.Conditions {
			conds.Member = append(conds.Member, toConditionXML(rule.Conditions[i]))
		}

		out.Conditions = conds
	}

	out.Actions = toActionsXML(rule.Actions)

	return out
}

// toConditionXML renders a driver rule condition, echoing both the deprecated
// flat Values and whichever typed config the condition carries.
//
//nolint:gocritic // hugeParam: value receiver keeps the call site simple; copy cost is negligible.
func toConditionXML(c lbdriver.RuleCondition) ruleConditionXML {
	x := ruleConditionXML{Field: c.Field}

	if len(c.Values) > 0 {
		x.Values = &stringListXML{Member: c.Values}
	}

	if c.HostHeaderConfig != nil {
		x.HostHeaderConfig = &valuesConfigXML{Values: &stringListXML{Member: c.HostHeaderConfig.Values}}
	}

	if c.PathPatternConfig != nil {
		x.PathPatternConfig = &valuesConfigXML{Values: &stringListXML{Member: c.PathPatternConfig.Values}}
	}

	if c.SourceIPConfig != nil {
		x.SourceIPConfig = &valuesConfigXML{Values: &stringListXML{Member: c.SourceIPConfig.Values}}
	}

	if c.HTTPRequestMethodConfig != nil {
		x.HTTPRequestMethodConfig = &valuesConfigXML{Values: &stringListXML{Member: c.HTTPRequestMethodConfig.Values}}
	}

	if hc := c.HTTPHeaderConfig; hc != nil {
		x.HTTPHeaderConfig = &httpHeaderConfigXML{
			HTTPHeaderName: hc.HTTPHeaderName,
			Values:         &stringListXML{Member: hc.Values},
		}
	}

	if qc := c.QueryStringConfig; qc != nil {
		x.QueryStringConfig = toQueryStringConfigXML(qc)
	}

	return x
}

// toQueryStringConfigXML renders a query-string condition's key/value pairs.
func toQueryStringConfigXML(qc *lbdriver.QueryStringConditionConfig) *queryStringConfigXML {
	out := &queryStringConfigXML{Values: &queryStringValuesXML{}}
	for _, kv := range qc.Values {
		out.Values.Member = append(out.Values.Member, queryStringKVXML{Key: kv.Key, Value: kv.Value})
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

// formInt32 parses a form value as an int32, returning 0 for an empty or
// malformed value. Used for the bounded Weight field of a weighted forward.
func formInt32(v string) int32 {
	if v == "" {
		return 0
	}

	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0
	}

	return int32(n)
}
