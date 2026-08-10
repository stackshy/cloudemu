// Package route53resolver implements the AWS Route 53 Resolver control-plane
// API (AWS JSON 1.1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/route53resolver client at a Server registered with this
// handler and the operations work end-to-end against an in-memory driver.
//
// Route 53 Resolver uses the AWS JSON 1.1 wire shape (POST + JSON body,
// dispatched on the X-Amz-Target header with the prefix "Route53Resolver.").
// The Matches predicate is scoped to that prefix so it never shadows other
// handlers.
package route53resolver

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

const targetPrefix = "Route53Resolver."

// Handler serves Route 53 Resolver requests against a driver.
type Handler struct {
	r53r driver.Route53Resolver
}

// New returns a Route 53 Resolver handler backed by d.
func New(d driver.Route53Resolver) *Handler {
	return &Handler{r53r: d}
}

// Matches returns true for Route 53 Resolver requests, identified by an
// X-Amz-Target header of "Route53Resolver.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches operations based on the X-Amz-Target suffix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	fn, ok := h.routes()[op]
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException", "unknown operation: "+op)

		return
	}

	fn(w, r)
}

// routes maps each supported operation to its handler.
func (h *Handler) routes() map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		// Resolver endpoints
		"CreateResolverEndpoint":                h.createResolverEndpoint,
		"GetResolverEndpoint":                   h.getResolverEndpoint,
		"UpdateResolverEndpoint":                h.updateResolverEndpoint,
		"DeleteResolverEndpoint":                h.deleteResolverEndpoint,
		"ListResolverEndpoints":                 h.listResolverEndpoints,
		"AssociateResolverEndpointIpAddress":    h.associateResolverEndpointIP,
		"DisassociateResolverEndpointIpAddress": h.disassociateResolverEndpointIP,
		"ListResolverEndpointIpAddresses":       h.listResolverEndpointIPs,
		// Resolver rules
		"CreateResolverRule":           h.createResolverRule,
		"GetResolverRule":              h.getResolverRule,
		"UpdateResolverRule":           h.updateResolverRule,
		"DeleteResolverRule":           h.deleteResolverRule,
		"ListResolverRules":            h.listResolverRules,
		"AssociateResolverRule":        h.associateResolverRule,
		"DisassociateResolverRule":     h.disassociateResolverRule,
		"GetResolverRuleAssociation":   h.getResolverRuleAssociation,
		"ListResolverRuleAssociations": h.listResolverRuleAssociations,
		"PutResolverRulePolicy":        h.putResolverRulePolicy,
		"GetResolverRulePolicy":        h.getResolverRulePolicy,
		// Query-log configs
		"CreateResolverQueryLogConfig":           h.createQueryLogConfig,
		"GetResolverQueryLogConfig":              h.getQueryLogConfig,
		"DeleteResolverQueryLogConfig":           h.deleteQueryLogConfig,
		"ListResolverQueryLogConfigs":            h.listQueryLogConfigs,
		"AssociateResolverQueryLogConfig":        h.associateQueryLogConfig,
		"DisassociateResolverQueryLogConfig":     h.disassociateQueryLogConfig,
		"GetResolverQueryLogConfigAssociation":   h.getQueryLogConfigAssociation,
		"ListResolverQueryLogConfigAssociations": h.listQueryLogConfigAssociations,
		"PutResolverQueryLogConfigPolicy":        h.putQueryLogConfigPolicy,
		"GetResolverQueryLogConfigPolicy":        h.getQueryLogConfigPolicy,
		// Resolver configs
		"GetResolverConfig":          h.getResolverConfig,
		"UpdateResolverConfig":       h.updateResolverConfig,
		"ListResolverConfigs":        h.listResolverConfigs,
		"GetResolverDnssecConfig":    h.getResolverDnssecConfig,
		"UpdateResolverDnssecConfig": h.updateResolverDnssecConfig,
		"ListResolverDnssecConfigs":  h.listResolverDnssecConfigs,
		// DNS Firewall — domain lists
		"CreateFirewallDomainList": h.createFirewallDomainList,
		"GetFirewallDomainList":    h.getFirewallDomainList,
		"DeleteFirewallDomainList": h.deleteFirewallDomainList,
		"ListFirewallDomainLists":  h.listFirewallDomainLists,
		"UpdateFirewallDomains":    h.updateFirewallDomains,
		"ImportFirewallDomains":    h.importFirewallDomains,
		"ListFirewallDomains":      h.listFirewallDomains,
		// DNS Firewall — rules
		"CreateFirewallRule":      h.createFirewallRule,
		"UpdateFirewallRule":      h.updateFirewallRule,
		"DeleteFirewallRule":      h.deleteFirewallRule,
		"ListFirewallRules":       h.listFirewallRules,
		"BatchCreateFirewallRule": h.batchCreateFirewallRule,
		"BatchUpdateFirewallRule": h.batchUpdateFirewallRule,
		"BatchDeleteFirewallRule": h.batchDeleteFirewallRule,
		// DNS Firewall — rule groups
		"CreateFirewallRuleGroup":    h.createFirewallRuleGroup,
		"GetFirewallRuleGroup":       h.getFirewallRuleGroup,
		"DeleteFirewallRuleGroup":    h.deleteFirewallRuleGroup,
		"ListFirewallRuleGroups":     h.listFirewallRuleGroups,
		"PutFirewallRuleGroupPolicy": h.putFirewallRuleGroupPolicy,
		"GetFirewallRuleGroupPolicy": h.getFirewallRuleGroupPolicy,
		// DNS Firewall — rule-group associations
		"AssociateFirewallRuleGroup":         h.associateFirewallRuleGroup,
		"DisassociateFirewallRuleGroup":      h.disassociateFirewallRuleGroup,
		"GetFirewallRuleGroupAssociation":    h.getFirewallRuleGroupAssociation,
		"ListFirewallRuleGroupAssociations":  h.listFirewallRuleGroupAssociations,
		"UpdateFirewallRuleGroupAssociation": h.updateFirewallRuleGroupAssociation,
		// DNS Firewall — configs + rule types
		"GetFirewallConfig":     h.getFirewallConfig,
		"UpdateFirewallConfig":  h.updateFirewallConfig,
		"ListFirewallConfigs":   h.listFirewallConfigs,
		"ListFirewallRuleTypes": h.listFirewallRuleTypes,
		// Outpost resolvers
		"CreateOutpostResolver": h.createOutpostResolver,
		"GetOutpostResolver":    h.getOutpostResolver,
		"UpdateOutpostResolver": h.updateOutpostResolver,
		"DeleteOutpostResolver": h.deleteOutpostResolver,
		"ListOutpostResolvers":  h.listOutpostResolvers,
		// Tagging
		"TagResource":         h.tagResource,
		"UntagResource":       h.untagResource,
		"ListTagsForResource": h.listTagsForResource,
	}
}
