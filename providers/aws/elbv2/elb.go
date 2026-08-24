// Package elbv2 provides an in-memory mock implementation of AWS Elastic Load Balancing.
package elbv2

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Compile-time checks that Mock implements driver.LoadBalancer and the optional
// modifier extensions the ELBv2 wire handler dispatches to.
var (
	_ driver.LoadBalancer              = (*Mock)(nil)
	_ driver.TargetGroupModifier       = (*Mock)(nil)
	_ driver.RuleModifier              = (*Mock)(nil)
	_ driver.LBNetworkModifier         = (*Mock)(nil)
	_ driver.ListenerGetter            = (*Mock)(nil)
	_ driver.TargetGroupAttributeStore = (*Mock)(nil)
)

// defaultIdleTimeoutSec is the default idle timeout for load balancers in seconds.
const defaultIdleTimeoutSec = 60

// Mock is an in-memory mock implementation of the AWS ELB service.
type Mock struct {
	lbs       *memstore.Store[driver.LBInfo]
	tgs       *memstore.Store[driver.TargetGroupInfo]
	listeners *memstore.Store[driver.ListenerInfo]
	rules     *memstore.Store[driver.RuleInfo]
	opts      *config.Options

	healthMu sync.RWMutex
	health   map[string]map[string]*driver.TargetHealth // tgARN -> targetID -> health

	attrsMu sync.RWMutex
	attrs   map[string]driver.LBAttributes // lbARN -> attributes

	tgAttrsMu sync.RWMutex
	tgAttrs   map[string]map[string]string // tgARN -> attribute overrides
}

// New creates a new ELB mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		lbs:       memstore.New[driver.LBInfo](),
		tgs:       memstore.New[driver.TargetGroupInfo](),
		listeners: memstore.New[driver.ListenerInfo](),
		rules:     memstore.New[driver.RuleInfo](),
		opts:      opts,
		health:    make(map[string]map[string]*driver.TargetHealth),
		attrs:     make(map[string]driver.LBAttributes),
		tgAttrs:   make(map[string]map[string]string),
	}
}

// CreateLoadBalancer creates a new load balancer.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLoadBalancer(_ context.Context, cfg driver.LBConfig) (*driver.LBInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "load balancer name is required")
	}

	// Real ELBv2 rejects a second load balancer with the same name in the
	// account/region with DuplicateLoadBalancerName.
	for _, existing := range m.lbs.All() {
		if existing.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists,
				"load balancer %q already exists", cfg.Name)
		}
	}

	id := idgen.GenerateID("lb-")
	arn := idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID, "loadbalancer/"+cfg.Name)
	dnsName := fmt.Sprintf("%s.%s.elb.amazonaws.com", cfg.Name, m.opts.Region)

	subnets := make([]string, len(cfg.Subnets))
	copy(subnets, cfg.Subnets)

	sgs := make([]string, len(cfg.SecurityGroups))
	copy(sgs, cfg.SecurityGroups)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	ipType := cfg.IPAddressType
	if ipType == "" {
		ipType = "ipv4"
	}

	lb := driver.LBInfo{
		ID:                    id,
		ARN:                   arn,
		Name:                  cfg.Name,
		Type:                  cfg.Type,
		Scheme:                cfg.Scheme,
		State:                 "active",
		DNSName:               dnsName,
		Subnets:               subnets,
		SecurityGroups:        sgs,
		IPAddressType:         ipType,
		CanonicalHostedZoneID: canonicalHostedZoneID(m.opts.Region),
		CreatedTime:           m.opts.Clock.Now().UTC(),
		Tags:                  tags,
	}

	m.lbs.Set(arn, lb)

	result := lb

	return &result, nil
}

// elbHostedZones maps a region to the canonical hosted-zone id an application
// load balancer exposes for Route 53 alias records. Values match the real AWS
// ELB service; regions outside the table fall back to us-east-1's id so an
// alias record can still be constructed.
//
//nolint:gochecknoglobals // static lookup table of AWS-published constants.
var elbHostedZones = map[string]string{
	"us-east-1":      "Z35SXDOTRQ7X7K",
	"us-east-2":      "Z3AADJGX6KTTL2",
	"us-west-1":      "Z368ELLRRE2KJ0",
	"us-west-2":      "Z1H1FL5HABSF5",
	"eu-west-1":      "Z32O12XQLNTSW2",
	"eu-central-1":   "Z215JYRZR1TBD5",
	"ap-south-1":     "ZP97RAFLXTNZK",
	"ap-southeast-1": "Z1LMS91P8CMLE5",
	"ap-southeast-2": "Z1GM3OXH4ZPM65",
	"ap-northeast-1": "Z14GRHDCWA56QT",
}

// canonicalHostedZoneID returns the ELB canonical hosted-zone id for region.
func canonicalHostedZoneID(region string) string {
	if z, ok := elbHostedZones[region]; ok {
		return z
	}

	return elbHostedZones["us-east-1"]
}

// DeleteLoadBalancer deletes a load balancer by ARN.
//
// ELBv2 DeleteLoadBalancer is idempotent: per the API reference, "if the load
// balancer does not exist or has already been deleted, the call succeeds." So a
// second delete (or a delete of an ARN that never existed) returns no error,
// which keeps idempotent teardown flows (terraform destroy retries) working.
func (m *Mock) DeleteLoadBalancer(_ context.Context, arn string) error {
	m.lbs.Delete(arn)

	// Delete all listeners associated with this load balancer.
	all := m.listeners.All()
	for key, li := range all {
		if li.LBARN == arn {
			m.listeners.Delete(key)
		}
	}

	return nil
}

// DescribeLoadBalancers returns load balancers matching the given ARNs.
// If arns is empty, all load balancers are returned.
//
// Naming an ARN that does not exist is LoadBalancerNotFound, not an empty
// list. Callers waiting for a delete to settle poll this until it errors, so
// answering "no error, nothing found" leaves them polling to their timeout
// over a load balancer that is already gone.
func (m *Mock) DescribeLoadBalancers(_ context.Context, arns []string) ([]driver.LBInfo, error) {
	for _, arn := range arns {
		if !m.lbs.Has(arn) {
			return nil, errors.Newf(errors.NotFound,
				"load balancer %q not found", arn)
		}
	}

	return describeResources(m.lbs, arns), nil
}

// CreateTargetGroup creates a new target group.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateTargetGroup(_ context.Context, cfg driver.TargetGroupConfig) (*driver.TargetGroupInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "target group name is required")
	}

	// Real ELBv2 rejects a second target group with the same name in the
	// account/region with DuplicateTargetGroupName.
	for _, existing := range m.tgs.All() {
		if existing.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists,
				"target group %q already exists", cfg.Name)
		}
	}

	id := idgen.GenerateID("tg-")
	arn := idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID, "targetgroup/"+cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	targetType := cfg.TargetType
	if targetType == "" {
		targetType = "instance"
	}

	hc := defaultHealthCheck(cfg)

	tg := driver.TargetGroupInfo{
		ID:          id,
		ARN:         arn,
		Name:        cfg.Name,
		Protocol:    cfg.Protocol,
		Port:        cfg.Port,
		VPCID:       cfg.VPCID,
		TargetType:  targetType,
		HealthPath:  hc.Path,
		HealthCheck: hc,
		Tags:        tags,
	}

	m.tgs.Set(arn, tg)

	// Initialize health map for this target group.
	m.healthMu.Lock()
	m.health[arn] = make(map[string]*driver.TargetHealth)
	m.healthMu.Unlock()

	result := tg

	return &result, nil
}

// Health-check defaults ELBv2 applies when a create request leaves a field
// unset.
const (
	defaultHCIntervalSec   = 30
	defaultHCTimeoutSec    = 5
	defaultHCHealthyCount  = 5
	defaultHCUnhealthyCoun = 2
	defaultHCMatcher       = "200"
	defaultHCPort          = "traffic-port"
	defaultHCPath          = "/"
)

// isHTTPProtocol reports whether p is an HTTP-family protocol (which carries a
// path and an HTTP matcher on its health check).
func isHTTPProtocol(p string) bool {
	return p == "HTTP" || p == "HTTPS"
}

// defaultHealthCheck fills a target group's health-check settings, applying the
// ELBv2 protocol-derived defaults for any field the caller left unset.
//
//nolint:gocritic // hugeParam: called once per create, copy cost is irrelevant.
func defaultHealthCheck(cfg driver.TargetGroupConfig) driver.HealthCheck {
	hc := cfg.HealthCheck

	if hc.Protocol == "" {
		hc.Protocol = cfg.Protocol
	}

	if hc.Port == "" {
		hc.Port = defaultHCPort
	}

	if hc.Path == "" && isHTTPProtocol(hc.Protocol) {
		hc.Path = cfg.HealthPath
		if hc.Path == "" {
			hc.Path = defaultHCPath
		}
	}

	if hc.IntervalSeconds == 0 {
		hc.IntervalSeconds = defaultHCIntervalSec
	}

	if hc.TimeoutSeconds == 0 {
		hc.TimeoutSeconds = defaultHCTimeoutSec
	}

	if hc.HealthyThreshold == 0 {
		hc.HealthyThreshold = defaultHCHealthyCount
	}

	if hc.UnhealthyThreshold == 0 {
		hc.UnhealthyThreshold = defaultHCUnhealthyCoun
	}

	if hc.Matcher == "" && isHTTPProtocol(hc.Protocol) {
		hc.Matcher = defaultHCMatcher
	}

	return hc
}

// DeleteTargetGroup deletes a target group by ARN.
//
// Per the API reference, a target group can be deleted only if it is not
// referenced by any actions. A target group that is still the forward target of
// a listener default action or a rule action fails with ResourceInUse.
func (m *Mock) DeleteTargetGroup(_ context.Context, arn string) error {
	if !m.tgs.Has(arn) {
		return errors.Newf(errors.NotFound, "target group %q not found", arn)
	}

	if err := m.checkTargetGroupNotInUse(arn); err != nil {
		return err
	}

	m.tgs.Delete(arn)

	// Clean up health data.
	m.healthMu.Lock()
	delete(m.health, arn)
	m.healthMu.Unlock()

	// Clean up any stored attribute overrides.
	m.tgAttrsMu.Lock()
	delete(m.tgAttrs, arn)
	m.tgAttrsMu.Unlock()

	return nil
}

// checkTargetGroupNotInUse reports ResourceInUse (via FailedPrecondition) when a
// target group is still referenced by a listener default action or a rule
// action, so a delete cannot silently orphan a forward target.
func (m *Mock) checkTargetGroupNotInUse(arn string) error {
	for _, li := range m.listeners.All() {
		if li.TargetGroupARN == arn {
			return errors.Newf(errors.FailedPrecondition,
				"target group %q is currently in use by a listener", arn)
		}
	}

	for _, r := range m.rules.All() {
		for _, a := range r.Actions {
			if a.TargetGroupARN == arn {
				return errors.Newf(errors.FailedPrecondition,
					"target group %q is currently in use by a rule", arn)
			}
		}
	}

	return nil
}

// DescribeTargetGroups returns target groups matching the given ARNs.
// If arns is empty, all target groups are returned.
//
// Naming an ARN that does not exist is TargetGroupNotFound, for the same
// reason DescribeLoadBalancers above answers LoadBalancerNotFound: a caller
// waiting on a delete polls until it errors, and an empty list with no error
// leaves it polling to its timeout over something already gone.
func (m *Mock) DescribeTargetGroups(_ context.Context, arns []string) ([]driver.TargetGroupInfo, error) {
	for _, arn := range arns {
		if !m.tgs.Has(arn) {
			return nil, errors.Newf(errors.NotFound,
				"target group %q not found", arn)
		}
	}

	return describeResources(m.tgs, arns), nil
}

// describeResources is a generic helper for Describe* methods that list or filter by keys.
func describeResources[T any](store *memstore.Store[T], keys []string) []T {
	if len(keys) == 0 {
		all := store.All()
		results := make([]T, 0, len(all))

		for _, item := range all {
			results = append(results, item)
		}

		return results
	}

	results := make([]T, 0, len(keys))

	for _, key := range keys {
		item, ok := store.Get(key)
		if !ok {
			continue
		}

		results = append(results, item)
	}

	return results
}

// filterToSlice returns a slice of values from the store that match the predicate.
func filterToSlice[T any](store *memstore.Store[T], pred func(string, T) bool) []T {
	filtered := store.Filter(pred)

	results := make([]T, 0, len(filtered))
	for _, item := range filtered {
		results = append(results, item)
	}

	return results
}

// CreateListener creates a new listener on a load balancer.
func (m *Mock) CreateListener(_ context.Context, cfg driver.ListenerConfig) (*driver.ListenerInfo, error) {
	lb, ok := m.lbs.Get(cfg.LBARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", cfg.LBARN)
	}

	// A default action that forwards to a target group must reference one that
	// exists; real ELBv2 rejects a bogus TargetGroupArn with TargetGroupNotFound.
	if cfg.TargetGroupARN != "" && !m.tgs.Has(cfg.TargetGroupARN) {
		return nil, errors.Newf(errors.NotFound, "target group %q not found", cfg.TargetGroupARN)
	}

	// A listener ARN embeds the load balancer's resource path plus a unique
	// listener id, never the full load balancer ARN (which would nest a second
	// "arn:" inside the value and break ARN parsers).
	listenerID := idgen.GenerateID("")
	arn := idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID,
		fmt.Sprintf("listener/%s/%s", lb.Name, listenerID))

	li := driver.ListenerInfo{
		ARN:            arn,
		LBARN:          cfg.LBARN,
		Protocol:       cfg.Protocol,
		Port:           cfg.Port,
		TargetGroupARN: cfg.TargetGroupARN,
	}

	m.listeners.Set(arn, li)

	result := li

	return &result, nil
}

// DeleteListener deletes a listener by ARN.
func (m *Mock) DeleteListener(_ context.Context, arn string) error {
	if !m.listeners.Delete(arn) {
		return errors.Newf(errors.NotFound, "listener %q not found", arn)
	}

	return nil
}

// GetListener returns a single listener by ARN.
func (m *Mock) GetListener(_ context.Context, listenerARN string) (*driver.ListenerInfo, error) {
	li, ok := m.listeners.Get(listenerARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "listener %q not found", listenerARN)
	}

	result := li

	return &result, nil
}

// DescribeListeners returns all listeners for the specified load balancer.
func (m *Mock) DescribeListeners(_ context.Context, lbARN string) ([]driver.ListenerInfo, error) {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	return filterToSlice(m.listeners, func(_ string, li driver.ListenerInfo) bool {
		return li.LBARN == lbARN
	}), nil
}

// CreateRule creates a new listener rule.
func (m *Mock) CreateRule(_ context.Context, cfg driver.RuleConfig) (*driver.RuleInfo, error) {
	if _, ok := m.listeners.Get(cfg.ListenerARN); !ok {
		return nil, errors.Newf(errors.NotFound, "listener %q not found", cfg.ListenerARN)
	}

	// A forward action must reference an existing target group; real ELBv2
	// rejects a bogus TargetGroupArn with TargetGroupNotFound.
	for _, a := range cfg.Actions {
		if a.TargetGroupARN != "" && !m.tgs.Has(a.TargetGroupARN) {
			return nil, errors.Newf(errors.NotFound, "target group %q not found", a.TargetGroupARN)
		}
	}

	// A listener can't have two rules with the same priority; a reused priority
	// fails with PriorityInUse.
	if cfg.Priority != 0 {
		for _, r := range m.rules.All() {
			if r.ListenerARN == cfg.ListenerARN && r.Priority == cfg.Priority {
				return nil, errors.Newf(errors.FailedPrecondition,
					"priority %d is currently in use", cfg.Priority)
			}
		}
	}

	arn := m.ruleARN(cfg.ListenerARN)

	conditions := make([]driver.RuleCondition, len(cfg.Conditions))
	copy(conditions, cfg.Conditions)

	actions := make([]driver.RuleAction, len(cfg.Actions))
	copy(actions, cfg.Actions)

	rule := driver.RuleInfo{
		ARN:         arn,
		ListenerARN: cfg.ListenerARN,
		Priority:    cfg.Priority,
		Conditions:  conditions,
		Actions:     actions,
		IsDefault:   false,
	}

	m.rules.Set(arn, rule)

	result := rule

	return &result, nil
}

// ruleARN builds a rule ARN from the listener's resource path so it reads
// arn:aws:elasticloadbalancing:REGION:ACCT:listener-rule/<lb>/<listener-id>/<rule-id>
// — resource type "listener-rule" with a single "arn:" prefix, never nesting the
// full listener ARN inside the value (which breaks ARN parsers).
func (m *Mock) ruleARN(listenerARN string) string {
	ruleID := idgen.GenerateID("")
	resource := "listener-rule/" + ruleID

	if idx := strings.Index(listenerARN, ":listener/"); idx != -1 {
		path := listenerARN[idx+len(":listener/"):]
		resource = "listener-rule/" + path + "/" + ruleID
	}

	return idgen.AWSARN("elasticloadbalancing", m.opts.Region, m.opts.AccountID, resource)
}

// DeleteRule deletes a listener rule by ARN.
func (m *Mock) DeleteRule(_ context.Context, ruleARN string) error {
	if !m.rules.Delete(ruleARN) {
		return errors.Newf(errors.NotFound, "rule %q not found", ruleARN)
	}

	return nil
}

// DescribeRules returns all rules for the specified listener.
func (m *Mock) DescribeRules(_ context.Context, listenerARN string) ([]driver.RuleInfo, error) {
	if _, ok := m.listeners.Get(listenerARN); !ok {
		return nil, errors.Newf(errors.NotFound, "listener %q not found", listenerARN)
	}

	return filterToSlice(m.rules, func(_ string, r driver.RuleInfo) bool {
		return r.ListenerARN == listenerARN
	}), nil
}

// ModifyListener modifies an existing listener's port, protocol, or default actions.
func (m *Mock) ModifyListener(_ context.Context, input driver.ModifyListenerInput) error {
	li, ok := m.listeners.Get(input.ListenerARN)
	if !ok {
		return errors.Newf(errors.NotFound, "listener %q not found", input.ListenerARN)
	}

	if input.Port != 0 {
		li.Port = input.Port
	}

	if input.Protocol != "" {
		li.Protocol = input.Protocol
	}

	if len(input.DefaultActions) > 0 {
		li.TargetGroupARN = input.DefaultActions[0].TargetGroupARN
	}

	m.listeners.Set(input.ListenerARN, li)

	return nil
}

// ModifyTargetGroup applies a partial health-check update to an existing target
// group, leaving any field the caller omitted unchanged.
//
//nolint:gocritic // hugeParam: interface method signature is fixed.
func (m *Mock) ModifyTargetGroup(
	_ context.Context, input driver.ModifyTargetGroupInput,
) (*driver.TargetGroupInfo, error) {
	tg, ok := m.tgs.Get(input.TargetGroupARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "target group %q not found", input.TargetGroupARN)
	}

	applyHealthCheckUpdate(&tg.HealthCheck, input)
	tg.HealthPath = tg.HealthCheck.Path

	m.tgs.Set(input.TargetGroupARN, tg)

	result := tg

	return &result, nil
}

// applyHealthCheckUpdate overlays the set fields of a ModifyTargetGroup request
// onto an existing health check.
//
//nolint:gocritic // hugeParam: called once per modify, copy cost is irrelevant.
func applyHealthCheckUpdate(hc *driver.HealthCheck, input driver.ModifyTargetGroupInput) {
	if input.HealthCheckProto != "" {
		hc.Protocol = input.HealthCheckProto
	}

	if input.HealthCheckPort != "" {
		hc.Port = input.HealthCheckPort
	}

	if input.HealthCheckPath != "" {
		hc.Path = input.HealthCheckPath
	}

	if input.IntervalSeconds != 0 {
		hc.IntervalSeconds = input.IntervalSeconds
	}

	if input.TimeoutSeconds != 0 {
		hc.TimeoutSeconds = input.TimeoutSeconds
	}

	if input.HealthyThreshold != 0 {
		hc.HealthyThreshold = input.HealthyThreshold
	}

	if input.UnhealthyThreshold != 0 {
		hc.UnhealthyThreshold = input.UnhealthyThreshold
	}

	if input.Matcher != "" {
		hc.Matcher = input.Matcher
	}
}

// ModifyRule replaces the conditions and/or actions of an existing rule.
//
//nolint:gocritic // hugeParam: interface method signature is fixed.
func (m *Mock) ModifyRule(_ context.Context, input driver.ModifyRuleInput) (*driver.RuleInfo, error) {
	rule, ok := m.rules.Get(input.RuleARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "rule %q not found", input.RuleARN)
	}

	if len(input.Conditions) > 0 {
		rule.Conditions = append([]driver.RuleCondition(nil), input.Conditions...)
	}

	if len(input.Actions) > 0 {
		rule.Actions = append([]driver.RuleAction(nil), input.Actions...)
	}

	m.rules.Set(input.RuleARN, rule)

	result := rule

	return &result, nil
}

// SetRulePriorities reassigns the priorities of the named rules and returns the
// updated rules.
func (m *Mock) SetRulePriorities(
	_ context.Context, pairs []driver.RulePriorityPair,
) ([]driver.RuleInfo, error) {
	out := make([]driver.RuleInfo, 0, len(pairs))

	for _, p := range pairs {
		rule, ok := m.rules.Get(p.RuleARN)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "rule %q not found", p.RuleARN)
		}

		rule.Priority = p.Priority
		m.rules.Set(p.RuleARN, rule)
		out = append(out, rule)
	}

	return out, nil
}

// SetSecurityGroups replaces the security groups attached to a load balancer.
func (m *Mock) SetSecurityGroups(_ context.Context, lbARN string, securityGroups []string) error {
	lb, ok := m.lbs.Get(lbARN)
	if !ok {
		return errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	lb.SecurityGroups = append([]string(nil), securityGroups...)
	m.lbs.Set(lbARN, lb)

	return nil
}

// SetSubnets replaces the subnets a load balancer is attached to and returns
// the resulting subnet list.
func (m *Mock) SetSubnets(_ context.Context, lbARN string, subnets []string) ([]string, error) {
	lb, ok := m.lbs.Get(lbARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	lb.Subnets = append([]string(nil), subnets...)
	m.lbs.Set(lbARN, lb)

	return lb.Subnets, nil
}

// GetLBAttributes returns the attributes for a load balancer.
func (m *Mock) GetLBAttributes(_ context.Context, lbARN string) (*driver.LBAttributes, error) {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	m.attrsMu.RLock()
	defer m.attrsMu.RUnlock()

	attrs, ok := m.attrs[lbARN]
	if !ok {
		attrs = driver.LBAttributes{IdleTimeout: defaultIdleTimeoutSec}
	}

	// Extra is a map, so the struct copy above still aliases the stored one.
	// A caller reading attributes, mutating Extra, and writing them back —
	// which is what a partial attribute update does — would otherwise write
	// into the shared map outside this lock, and two overlapping updates on
	// one load balancer crash the process with a concurrent map write.
	attrs.Extra = copyStringMap(attrs.Extra)

	return &attrs, nil
}

// copyStringMap returns an independent copy, or nil for an empty input so an
// absent map does not become an empty one.
func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

// PutLBAttributes sets the attributes for a load balancer.
func (m *Mock) PutLBAttributes(_ context.Context, lbARN string, attrs driver.LBAttributes) error {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	// Copy on the way in for the same reason as on the way out: the caller
	// keeps its reference to Extra and must not be able to mutate stored state
	// after the write returns.
	attrs.Extra = copyStringMap(attrs.Extra)

	m.attrsMu.Lock()
	m.attrs[lbARN] = attrs
	m.attrsMu.Unlock()

	return nil
}

// UpdateLBAttributes applies a partial update without releasing the lock
// between the read and the write, so concurrent modifications compose instead
// of overwriting one another.
func (m *Mock) UpdateLBAttributes(
	_ context.Context, lbARN string, apply func(*driver.LBAttributes),
) (*driver.LBAttributes, error) {
	if _, ok := m.lbs.Get(lbARN); !ok {
		return nil, errors.Newf(errors.NotFound, "load balancer %q not found", lbARN)
	}

	m.attrsMu.Lock()
	defer m.attrsMu.Unlock()

	attrs, ok := m.attrs[lbARN]
	if !ok {
		attrs = driver.LBAttributes{IdleTimeout: defaultIdleTimeoutSec}
	}

	// Work on a private copy of Extra so the caller's mutations land on the
	// copy this method stores, never on a map another reader still holds.
	attrs.Extra = copyStringMap(attrs.Extra)
	if attrs.Extra == nil {
		attrs.Extra = map[string]string{}
	}

	apply(&attrs)

	m.attrs[lbARN] = attrs

	out := attrs
	out.Extra = copyStringMap(attrs.Extra)

	return &out, nil
}

// defaultTargetGroupAttributes are the attributes real ELBv2 reports for a
// freshly created target group before any ModifyTargetGroupAttributes call.
// Modifications overlay these; a Describe returns the merged set.
//
//nolint:gochecknoglobals // static table of AWS-published default attribute values.
var defaultTargetGroupAttributes = map[string]string{
	"deregistration_delay.timeout_seconds": "300",
	"stickiness.enabled":                   "false",
	"stickiness.type":                      "lb_cookie",
	"load_balancing.algorithm.type":        "round_robin",
	"slow_start.duration_seconds":          "0",
}

// GetTargetGroupAttributes returns the full attribute set for a target group:
// the ELBv2 defaults overlaid with any stored overrides.
func (m *Mock) GetTargetGroupAttributes(_ context.Context, targetGroupARN string) (map[string]string, error) {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return nil, errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.tgAttrsMu.RLock()
	defer m.tgAttrsMu.RUnlock()

	return mergedTargetGroupAttributes(m.tgAttrs[targetGroupARN]), nil
}

// ModifyTargetGroupAttributes merges updates into the target group's stored
// overrides and returns the resulting full attribute set.
func (m *Mock) ModifyTargetGroupAttributes(
	_ context.Context, targetGroupARN string, updates map[string]string,
) (map[string]string, error) {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return nil, errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.tgAttrsMu.Lock()
	defer m.tgAttrsMu.Unlock()

	overrides := m.tgAttrs[targetGroupARN]
	if overrides == nil {
		overrides = make(map[string]string, len(updates))
	}

	for k, v := range updates {
		overrides[k] = v
	}

	m.tgAttrs[targetGroupARN] = overrides

	return mergedTargetGroupAttributes(overrides), nil
}

// mergedTargetGroupAttributes returns a fresh map of the defaults overlaid with
// overrides, so the caller never aliases stored state.
func mergedTargetGroupAttributes(overrides map[string]string) map[string]string {
	out := make(map[string]string, len(defaultTargetGroupAttributes)+len(overrides))
	for k, v := range defaultTargetGroupAttributes {
		out[k] = v
	}

	for k, v := range overrides {
		out[k] = v
	}

	return out
}

// RegisterTargets registers targets with a target group.
func (m *Mock) RegisterTargets(_ context.Context, targetGroupARN string, targets []driver.Target) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		tgHealth = make(map[string]*driver.TargetHealth)
		m.health[targetGroupARN] = tgHealth
	}

	for _, t := range targets {
		tgHealth[t.ID] = &driver.TargetHealth{
			Target: driver.Target{
				ID:   t.ID,
				Port: t.Port,
			},
			State:       "initial",
			Reason:      "Elb.RegistrationInProgress",
			Description: "Target registration is in progress",
		}
	}

	return nil
}

// DeregisterTargets removes targets from a target group.
func (m *Mock) DeregisterTargets(_ context.Context, targetGroupARN string, targets []driver.Target) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return nil
	}

	for _, t := range targets {
		delete(tgHealth, t.ID)
	}

	return nil
}

// DescribeTargetHealth returns the health status of all targets in a target
// group. A freshly registered target reports "initial" on its first describe
// and then advances to "healthy", mirroring real ELBv2's registration
// transition so target-health waiters make progress instead of hanging on a
// permanently "initial" state.
func (m *Mock) DescribeTargetHealth(_ context.Context, targetGroupARN string) ([]driver.TargetHealth, error) {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return nil, errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return []driver.TargetHealth{}, nil
	}

	results := make([]driver.TargetHealth, 0, len(tgHealth))
	for _, th := range tgHealth {
		results = append(results, *th)

		// Advance after the state is captured so the caller sees "initial"
		// once and "healthy" on the next poll.
		if th.State == "initial" {
			th.State = "healthy"
			th.Reason = ""
			th.Description = ""
		}
	}

	return results, nil
}

// SetTargetHealth sets the health state of a specific target in a target group.
func (m *Mock) SetTargetHealth(_ context.Context, targetGroupARN, targetID, state string) error {
	if _, ok := m.tgs.Get(targetGroupARN); !ok {
		return errors.Newf(errors.NotFound, "target group %q not found", targetGroupARN)
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	tgHealth, ok := m.health[targetGroupARN]
	if !ok {
		return errors.Newf(errors.NotFound, "no targets registered in target group %q", targetGroupARN)
	}

	th, ok := tgHealth[targetID]
	if !ok {
		return errors.Newf(errors.NotFound, "target %q not found in target group %q", targetID, targetGroupARN)
	}

	th.State = state
	th.Reason = ""

	return nil
}
