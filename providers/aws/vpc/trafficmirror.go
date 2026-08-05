package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// defaultVirtualNetworkID is the VNI EC2 auto-assigns to a mirror session when
// the caller omits one.
const defaultVirtualNetworkID = 1

// Removable field names accepted by the Modify* RemoveFields parameter.
const (
	fieldDescription          = "description"
	fieldProtocol             = "protocol"
	fieldDestinationPortRange = "destination-port-range"
	fieldSourcePortRange      = "source-port-range"
	fieldPacketLength         = "packet-length"
	fieldVirtualNetworkID     = "virtual-network-id"
)

// ---- Traffic Mirror Targets ----

// CreateTrafficMirrorTarget creates a mirror target from an ENI, NLB, or GWLB
// endpoint.
func (m *Mock) CreateTrafficMirrorTarget(
	_ context.Context, cfg driver.TrafficMirrorTargetConfig,
) (*driver.TrafficMirrorTarget, error) {
	t := &driver.TrafficMirrorTarget{
		ID:                            idgen.GenerateID("tmt-"),
		Description:                   cfg.Description,
		NetworkInterfaceID:            cfg.NetworkInterfaceID,
		NetworkLoadBalancerARN:        cfg.NetworkLoadBalancerARN,
		GatewayLoadBalancerEndpointID: cfg.GatewayLoadBalancerEndpointID,
		Type:                          trafficMirrorTargetType(cfg),
		OwnerID:                       m.opts.AccountID,
		Tags:                          copyTags(cfg.Tags),
	}
	m.trafficMirrorTargets.Set(t.ID, t)

	out := cloneTrafficMirrorTarget(t)

	return &out, nil
}

// trafficMirrorTargetType derives the target type from whichever destination
// the caller supplied, matching how real EC2 infers it.
func trafficMirrorTargetType(cfg driver.TrafficMirrorTargetConfig) string {
	switch {
	case cfg.NetworkLoadBalancerARN != "":
		return "network-load-balancer"
	case cfg.GatewayLoadBalancerEndpointID != "":
		return "gateway-load-balancer-endpoint"
	default:
		return "network-interface"
	}
}

// DeleteTrafficMirrorTarget deletes a mirror target. Real EC2 refuses the
// delete while a session still references the target.
func (m *Mock) DeleteTrafficMirrorTarget(_ context.Context, id string) error {
	if !m.trafficMirrorTargets.Has(id) {
		return errors.Newf(errors.NotFound, "traffic mirror target %q not found", id)
	}

	if sid, inUse := m.sessionReferencingTarget(id); inUse {
		return errors.Newf(errors.FailedPrecondition,
			"DependencyViolation: traffic mirror session %q still references target %q", sid, id)
	}

	m.trafficMirrorTargets.Delete(id)

	return nil
}

// sessionReferencingTarget reports a session still bound to the given target.
func (m *Mock) sessionReferencingTarget(targetID string) (string, bool) {
	for _, s := range m.trafficMirrorSessions.All() {
		if s.TrafficMirrorTargetID == targetID {
			return s.ID, true
		}
	}

	return "", false
}

// sessionReferencingFilter reports a session still bound to the given filter.
func (m *Mock) sessionReferencingFilter(filterID string) (string, bool) {
	for _, s := range m.trafficMirrorSessions.All() {
		if s.TrafficMirrorFilterID == filterID {
			return s.ID, true
		}
	}

	return "", false
}

// DescribeTrafficMirrorTargets returns mirror targets matching ids.
func (m *Mock) DescribeTrafficMirrorTargets(_ context.Context, ids []string) ([]driver.TrafficMirrorTarget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.trafficMirrorTargets, ids, cloneTrafficMirrorTarget), nil
}

// ---- Traffic Mirror Filters ----

// CreateTrafficMirrorFilter creates an empty mirror filter.
func (m *Mock) CreateTrafficMirrorFilter(
	_ context.Context, description string, tags map[string]string,
) (*driver.TrafficMirrorFilter, error) {
	f := &driver.TrafficMirrorFilter{
		ID:              idgen.GenerateID("tmf-"),
		Description:     description,
		NetworkServices: []string{},
		IngressRules:    []driver.TrafficMirrorFilterRule{},
		EgressRules:     []driver.TrafficMirrorFilterRule{},
		Tags:            copyTags(tags),
	}
	m.trafficMirrorFilters.Set(f.ID, f)

	out := cloneTrafficMirrorFilter(f)

	return &out, nil
}

// DeleteTrafficMirrorFilter deletes a mirror filter. Real EC2 refuses the
// delete while a session still references the filter.
func (m *Mock) DeleteTrafficMirrorFilter(_ context.Context, id string) error {
	if !m.trafficMirrorFilters.Has(id) {
		return errors.Newf(errors.NotFound, "traffic mirror filter %q not found", id)
	}

	if sid, inUse := m.sessionReferencingFilter(id); inUse {
		return errors.Newf(errors.FailedPrecondition,
			"DependencyViolation: traffic mirror session %q still references filter %q", sid, id)
	}

	m.trafficMirrorFilters.Delete(id)

	return nil
}

// DescribeTrafficMirrorFilters returns mirror filters matching ids.
func (m *Mock) DescribeTrafficMirrorFilters(_ context.Context, ids []string) ([]driver.TrafficMirrorFilter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.trafficMirrorFilters, ids, cloneTrafficMirrorFilter), nil
}

// ModifyTrafficMirrorFilterNetworkServices adds/removes monitored network
// services on a filter.
func (m *Mock) ModifyTrafficMirrorFilterNetworkServices(
	_ context.Context, filterID string, add, remove []string,
) (*driver.TrafficMirrorFilter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, ok := m.trafficMirrorFilters.Get(filterID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "traffic mirror filter %q not found", filterID)
	}

	f.NetworkServices = applyStringSetChanges(f.NetworkServices, add, remove)

	out := cloneTrafficMirrorFilter(f)

	return &out, nil
}

// ---- Traffic Mirror Filter Rules ----

// CreateTrafficMirrorFilterRule adds a rule to a filter's ingress or egress set.
//
//nolint:gocritic // cfg is passed by value to satisfy the driver interface.
func (m *Mock) CreateTrafficMirrorFilterRule(
	_ context.Context, cfg driver.TrafficMirrorFilterRuleConfig,
) (*driver.TrafficMirrorFilterRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, ok := m.trafficMirrorFilters.Get(cfg.FilterID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "traffic mirror filter %q not found", cfg.FilterID)
	}

	rule := driver.TrafficMirrorFilterRule{
		ID:                   idgen.GenerateID("tmfr-"),
		FilterID:             cfg.FilterID,
		TrafficDirection:     cfg.TrafficDirection,
		RuleNumber:           cfg.RuleNumber,
		RuleAction:           cfg.RuleAction,
		Protocol:             cfg.Protocol,
		DestinationCIDR:      cfg.DestinationCIDR,
		SourceCIDR:           cfg.SourceCIDR,
		DestinationPortRange: clonePortRange(cfg.DestinationPortRange),
		SourcePortRange:      clonePortRange(cfg.SourcePortRange),
		Description:          cfg.Description,
	}

	if cfg.TrafficDirection == "egress" {
		f.EgressRules = append(f.EgressRules, rule)
	} else {
		f.IngressRules = append(f.IngressRules, rule)
	}

	out := cloneFilterRule(&rule)

	return &out, nil
}

// ModifyTrafficMirrorFilterRule updates an existing rule. Fields listed in
// removeFields are cleared; other provided fields overwrite.
//
//nolint:gocritic // cfg is passed by value to satisfy the driver interface.
func (m *Mock) ModifyTrafficMirrorFilterRule(
	_ context.Context, id string, cfg driver.TrafficMirrorFilterRuleConfig, removeFields []string,
) (*driver.TrafficMirrorFilterRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, rule := m.findFilterRule(id)
	if rule == nil {
		return nil, errors.Newf(errors.NotFound, "traffic mirror filter rule %q not found", id)
	}

	applyFilterRuleUpdate(rule, &cfg)
	applyFilterRuleRemovals(rule, removeFields)

	m.trafficMirrorFilters.Set(f.ID, f)

	out := cloneFilterRule(rule)

	return &out, nil
}

// findFilterRule returns the owning filter and a pointer to the rule with id,
// or (nil, nil) if not found. Caller holds mu.
func (m *Mock) findFilterRule(id string) (*driver.TrafficMirrorFilter, *driver.TrafficMirrorFilterRule) {
	for _, f := range m.trafficMirrorFilters.All() {
		for i := range f.IngressRules {
			if f.IngressRules[i].ID == id {
				return f, &f.IngressRules[i]
			}
		}

		for i := range f.EgressRules {
			if f.EgressRules[i].ID == id {
				return f, &f.EgressRules[i]
			}
		}
	}

	return nil, nil
}

func applyFilterRuleUpdate(rule *driver.TrafficMirrorFilterRule, cfg *driver.TrafficMirrorFilterRuleConfig) {
	if cfg.TrafficDirection != "" {
		rule.TrafficDirection = cfg.TrafficDirection
	}

	if cfg.RuleNumber != 0 {
		rule.RuleNumber = cfg.RuleNumber
	}

	if cfg.RuleAction != "" {
		rule.RuleAction = cfg.RuleAction
	}

	if cfg.Protocol != 0 {
		rule.Protocol = cfg.Protocol
	}

	if cfg.DestinationCIDR != "" {
		rule.DestinationCIDR = cfg.DestinationCIDR
	}

	if cfg.SourceCIDR != "" {
		rule.SourceCIDR = cfg.SourceCIDR
	}

	if cfg.DestinationPortRange != nil {
		rule.DestinationPortRange = clonePortRange(cfg.DestinationPortRange)
	}

	if cfg.SourcePortRange != nil {
		rule.SourcePortRange = clonePortRange(cfg.SourcePortRange)
	}

	if cfg.Description != "" {
		rule.Description = cfg.Description
	}
}

func applyFilterRuleRemovals(rule *driver.TrafficMirrorFilterRule, removeFields []string) {
	for _, field := range removeFields {
		switch field {
		case fieldDestinationPortRange:
			rule.DestinationPortRange = nil
		case fieldSourcePortRange:
			rule.SourcePortRange = nil
		case fieldProtocol:
			rule.Protocol = 0
		case fieldDescription:
			rule.Description = ""
		}
	}
}

// DeleteTrafficMirrorFilterRule removes a rule from its owning filter.
func (m *Mock) DeleteTrafficMirrorFilterRule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, f := range m.trafficMirrorFilters.All() {
		if removeRuleByID(&f.IngressRules, id) || removeRuleByID(&f.EgressRules, id) {
			m.trafficMirrorFilters.Set(f.ID, f)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "traffic mirror filter rule %q not found", id)
}

func removeRuleByID(rules *[]driver.TrafficMirrorFilterRule, id string) bool {
	for i := range *rules {
		if (*rules)[i].ID == id {
			*rules = append((*rules)[:i], (*rules)[i+1:]...)
			return true
		}
	}

	return false
}

// DescribeTrafficMirrorFilterRules returns rules for a filter (and optionally a
// specific rule-id subset).
func (m *Mock) DescribeTrafficMirrorFilterRules(
	_ context.Context, filterID string, ruleIDs []string,
) ([]driver.TrafficMirrorFilterRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var rules []driver.TrafficMirrorFilterRule

	for _, f := range m.trafficMirrorFilters.All() {
		if filterID != "" && f.ID != filterID {
			continue
		}

		rules = append(rules, f.IngressRules...)
		rules = append(rules, f.EgressRules...)
	}

	if len(ruleIDs) > 0 {
		rules = filterRulesByID(rules, ruleIDs)
	}

	out := make([]driver.TrafficMirrorFilterRule, 0, len(rules))
	for i := range rules {
		out = append(out, cloneFilterRule(&rules[i]))
	}

	return out, nil
}

func filterRulesByID(rules []driver.TrafficMirrorFilterRule, ids []string) []driver.TrafficMirrorFilterRule {
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}

	out := rules[:0]

	for i := range rules {
		if _, ok := want[rules[i].ID]; ok {
			out = append(out, rules[i])
		}
	}

	return out
}

// ---- Traffic Mirror Sessions ----

// CreateTrafficMirrorSession binds a source ENI to a target and filter.
//
//nolint:gocritic // cfg is passed by value to satisfy the driver interface.
func (m *Mock) CreateTrafficMirrorSession(
	_ context.Context, cfg driver.TrafficMirrorSessionConfig,
) (*driver.TrafficMirrorSession, error) {
	if !m.trafficMirrorTargets.Has(cfg.TrafficMirrorTargetID) {
		return nil, errors.Newf(errors.NotFound,
			"traffic mirror target %q not found", cfg.TrafficMirrorTargetID)
	}

	if !m.trafficMirrorFilters.Has(cfg.TrafficMirrorFilterID) {
		return nil, errors.Newf(errors.NotFound,
			"traffic mirror filter %q not found", cfg.TrafficMirrorFilterID)
	}

	vni := cfg.VirtualNetworkID
	if vni == 0 {
		vni = defaultVirtualNetworkID
	}

	s := &driver.TrafficMirrorSession{
		ID:                    idgen.GenerateID("tms-"),
		NetworkInterfaceID:    cfg.NetworkInterfaceID,
		TrafficMirrorTargetID: cfg.TrafficMirrorTargetID,
		TrafficMirrorFilterID: cfg.TrafficMirrorFilterID,
		PacketLength:          cfg.PacketLength,
		SessionNumber:         cfg.SessionNumber,
		VirtualNetworkID:      vni,
		Description:           cfg.Description,
		OwnerID:               m.opts.AccountID,
		Tags:                  copyTags(cfg.Tags),
	}
	m.trafficMirrorSessions.Set(s.ID, s)

	out := cloneTrafficMirrorSession(s)

	return &out, nil
}

// ModifyTrafficMirrorSession updates a session. Fields in removeFields are
// cleared; other provided fields overwrite.
//
//nolint:gocritic // cfg is passed by value to satisfy the driver interface.
func (m *Mock) ModifyTrafficMirrorSession(
	_ context.Context, id string, cfg driver.TrafficMirrorSessionConfig, removeFields []string,
) (*driver.TrafficMirrorSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.trafficMirrorSessions.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "traffic mirror session %q not found", id)
	}

	// Re-validate a re-pointed target/filter, matching Create — otherwise a
	// Modify could bind the session to a nonexistent target or filter.
	if cfg.TrafficMirrorTargetID != "" && !m.trafficMirrorTargets.Has(cfg.TrafficMirrorTargetID) {
		return nil, errors.Newf(errors.NotFound,
			"traffic mirror target %q not found", cfg.TrafficMirrorTargetID)
	}

	if cfg.TrafficMirrorFilterID != "" && !m.trafficMirrorFilters.Has(cfg.TrafficMirrorFilterID) {
		return nil, errors.Newf(errors.NotFound,
			"traffic mirror filter %q not found", cfg.TrafficMirrorFilterID)
	}

	applySessionUpdate(s, &cfg)
	applySessionRemovals(s, removeFields)

	out := cloneTrafficMirrorSession(s)

	return &out, nil
}

func applySessionUpdate(s *driver.TrafficMirrorSession, cfg *driver.TrafficMirrorSessionConfig) {
	if cfg.TrafficMirrorTargetID != "" {
		s.TrafficMirrorTargetID = cfg.TrafficMirrorTargetID
	}

	if cfg.TrafficMirrorFilterID != "" {
		s.TrafficMirrorFilterID = cfg.TrafficMirrorFilterID
	}

	if cfg.PacketLength != 0 {
		s.PacketLength = cfg.PacketLength
	}

	if cfg.SessionNumber != 0 {
		s.SessionNumber = cfg.SessionNumber
	}

	if cfg.VirtualNetworkID != 0 {
		s.VirtualNetworkID = cfg.VirtualNetworkID
	}

	if cfg.Description != "" {
		s.Description = cfg.Description
	}
}

func applySessionRemovals(s *driver.TrafficMirrorSession, removeFields []string) {
	for _, field := range removeFields {
		switch field {
		case fieldPacketLength:
			s.PacketLength = 0
		case fieldDescription:
			s.Description = ""
		case fieldVirtualNetworkID:
			s.VirtualNetworkID = 0
		}
	}
}

// DeleteTrafficMirrorSession deletes a mirror session.
func (m *Mock) DeleteTrafficMirrorSession(_ context.Context, id string) error {
	if !m.trafficMirrorSessions.Delete(id) {
		return errors.Newf(errors.NotFound, "traffic mirror session %q not found", id)
	}

	return nil
}

// DescribeTrafficMirrorSessions returns sessions matching ids.
func (m *Mock) DescribeTrafficMirrorSessions(_ context.Context, ids []string) ([]driver.TrafficMirrorSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.trafficMirrorSessions, ids, cloneTrafficMirrorSession), nil
}

// ---- clone helpers ----

func cloneTrafficMirrorTarget(t *driver.TrafficMirrorTarget) driver.TrafficMirrorTarget {
	out := *t
	out.Tags = copyTags(t.Tags)

	return out
}

func cloneTrafficMirrorFilter(f *driver.TrafficMirrorFilter) driver.TrafficMirrorFilter {
	out := *f
	out.Tags = copyTags(f.Tags)
	out.NetworkServices = append([]string(nil), f.NetworkServices...)
	out.IngressRules = cloneFilterRules(f.IngressRules)
	out.EgressRules = cloneFilterRules(f.EgressRules)

	return out
}

func cloneFilterRules(rules []driver.TrafficMirrorFilterRule) []driver.TrafficMirrorFilterRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]driver.TrafficMirrorFilterRule, 0, len(rules))
	for i := range rules {
		out = append(out, cloneFilterRule(&rules[i]))
	}

	return out
}

func cloneFilterRule(r *driver.TrafficMirrorFilterRule) driver.TrafficMirrorFilterRule {
	out := *r
	out.DestinationPortRange = clonePortRange(r.DestinationPortRange)
	out.SourcePortRange = clonePortRange(r.SourcePortRange)

	return out
}

func clonePortRange(p *driver.TrafficMirrorPortRange) *driver.TrafficMirrorPortRange {
	if p == nil {
		return nil
	}

	cp := *p

	return &cp
}

func cloneTrafficMirrorSession(s *driver.TrafficMirrorSession) driver.TrafficMirrorSession {
	out := *s
	out.Tags = copyTags(s.Tags)

	return out
}

// applyStringSetChanges returns base with add appended (deduped) and remove
// deleted, preserving order.
func applyStringSetChanges(base, add, remove []string) []string {
	drop := make(map[string]struct{}, len(remove))
	for _, r := range remove {
		drop[r] = struct{}{}
	}

	seen := make(map[string]struct{}, len(base)+len(add))
	out := make([]string, 0, len(base)+len(add))

	combined := make([]string, 0, len(base)+len(add))
	combined = append(combined, base...)
	combined = append(combined, add...)

	for _, v := range combined {
		if _, gone := drop[v]; gone {
			continue
		}

		if _, dup := seen[v]; dup {
			continue
		}

		seen[v] = struct{}{}

		out = append(out, v)
	}

	return out
}
