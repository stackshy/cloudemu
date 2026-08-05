// Package route53resolver provides an in-memory mock of the AWS Route 53
// Resolver control plane. It satisfies services/route53resolver/driver so the
// real aws-sdk-go-v2/service/route53resolver client works against it via the
// AWS server (AWS JSON 1.1, X-Amz-Target "Route53Resolver.<Op>").
package route53resolver

import (
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// Compile-time check that Mock implements the driver contract.
var _ driver.Route53Resolver = (*Mock)(nil)

const (
	statusOperational = "OPERATIONAL"
	statusDeleting    = "DELETING"
	ipStatusAttached  = "ATTACHED"

	directionInbound = "INBOUND"
)

// Mock is an in-memory mock of AWS Route 53 Resolver.
type Mock struct {
	endpoints    *memstore.Store[*driver.ResolverEndpoint]             // keyed by endpoint ID
	rules        *memstore.Store[*driver.ResolverRule]                 // keyed by rule ID
	ruleAssocs   *memstore.Store[*driver.ResolverRuleAssociation]      // keyed by association ID
	rulePolicies *memstore.Store[string]                               // keyed by resource ARN
	qlcs         *memstore.Store[*driver.QueryLogConfig]               // keyed by config ID
	qlcAssocs    *memstore.Store[*driver.QueryLogConfigAssociation]    // keyed by association ID
	qlcPolicies  *memstore.Store[string]                               // keyed by resource ARN
	rslvrConfigs *memstore.Store[*driver.ResolverConfig]               // keyed by VPC/resource ID
	dnssecCfgs   *memstore.Store[*driver.ResolverDnssecConfig]         // keyed by VPC/resource ID
	fwDomLists   *memstore.Store[*driver.FirewallDomainList]           // keyed by domain-list ID
	fwDomains    *memstore.Store[[]string]                             // keyed by domain-list ID
	fwRuleGroups *memstore.Store[*driver.FirewallRuleGroup]            // keyed by rule-group ID
	fwRules      *memstore.Store[*driver.FirewallRule]                 // keyed by group|domainList|qtype
	fwAssocs     *memstore.Store[*driver.FirewallRuleGroupAssociation] // keyed by association ID
	fwConfigs    *memstore.Store[*driver.FirewallConfig]               // keyed by VPC/resource ID
	fwPolicies   *memstore.Store[string]                               // keyed by rule-group ARN
	outposts     *memstore.Store[*driver.OutpostResolver]              // keyed by outpost-resolver ID
	tags         *memstore.Store[[]driver.Tag]                         // keyed by resource ARN
	opts         *config.Options
	mu           sync.Mutex // serializes read-modify-write on stored records
}

// New returns a Route 53 Resolver mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		endpoints:    memstore.New[*driver.ResolverEndpoint](),
		rules:        memstore.New[*driver.ResolverRule](),
		ruleAssocs:   memstore.New[*driver.ResolverRuleAssociation](),
		rulePolicies: memstore.New[string](),
		qlcs:         memstore.New[*driver.QueryLogConfig](),
		qlcAssocs:    memstore.New[*driver.QueryLogConfigAssociation](),
		qlcPolicies:  memstore.New[string](),
		rslvrConfigs: memstore.New[*driver.ResolverConfig](),
		dnssecCfgs:   memstore.New[*driver.ResolverDnssecConfig](),
		fwDomLists:   memstore.New[*driver.FirewallDomainList](),
		fwDomains:    memstore.New[[]string](),
		fwRuleGroups: memstore.New[*driver.FirewallRuleGroup](),
		fwRules:      memstore.New[*driver.FirewallRule](),
		fwAssocs:     memstore.New[*driver.FirewallRuleGroupAssociation](),
		fwConfigs:    memstore.New[*driver.FirewallConfig](),
		fwPolicies:   memstore.New[string](),
		outposts:     memstore.New[*driver.OutpostResolver](),
		tags:         memstore.New[[]driver.Tag](),
		opts:         opts,
	}
}

// now returns the current time (via the injectable clock) in RFC 3339.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// arn builds a route53resolver ARN for the given resource path.
func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("route53resolver", m.opts.Region, m.opts.AccountID, resource)
}

// cloneEndpoint deep-copies an endpoint so stored records never share a slice
// backing array with a returned value (copy-on-write read discipline).
func cloneEndpoint(e *driver.ResolverEndpoint) driver.ResolverEndpoint {
	out := *e
	out.SecurityGroupIDs = append([]string(nil), e.SecurityGroupIDs...)
	out.Protocols = append([]string(nil), e.Protocols...)
	out.IPAddresses = append([]driver.IPAddress(nil), e.IPAddresses...)

	return out
}

// copyTags returns a defensive copy of a tag slice.
func copyTags(t []driver.Tag) []driver.Tag {
	return append([]driver.Tag(nil), t...)
}

// cloneRule deep-copies a resolver rule (its TargetIPs slice is copied).
func cloneRule(r *driver.ResolverRule) driver.ResolverRule {
	out := *r
	out.TargetIPs = append([]driver.TargetAddress(nil), r.TargetIPs...)

	return out
}

// cloneAssoc copies a rule association (no reference-type fields).
func cloneAssoc(a *driver.ResolverRuleAssociation) driver.ResolverRuleAssociation {
	return *a
}

// sortedValues returns the store's values sorted by key, each deep-copied via
// clone — the shared List implementation for every resource group.
func sortedValues[T any](all map[string]*T, clone func(*T) T) []T {
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	out := make([]T, 0, len(all))
	for _, id := range ids {
		out = append(out, clone(all[id]))
	}

	return out
}
