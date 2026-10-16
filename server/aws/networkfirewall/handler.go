// Package networkfirewall implements the AWS Network Firewall control-plane API
// as a server.Handler. Network Firewall uses AWS JSON 1.0 with the X-Amz-Target
// prefix "NetworkFirewall_20201112.", so real aws-sdk-go-v2 networkfirewall
// clients configured with a custom endpoint hit this handler unchanged.
package networkfirewall

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const targetPrefix = "NetworkFirewall_20201112."

// Handler serves Network Firewall requests against a networkfirewall driver.
// Operation families beyond the base driver interface are optional capability
// interfaces; an operation whose family the driver lacks answers
// UnsupportedOperationException.
type Handler struct {
	db     nfdriver.NetworkFirewall
	routes map[string]http.HandlerFunc
}

// New returns a Network Firewall handler backed by db.
func New(db nfdriver.NetworkFirewall) *Handler {
	h := &Handler{db: db}
	h.routes = h.buildRoutes()

	return h
}

// Matches claims requests whose X-Amz-Target names a Network Firewall operation.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches on the operation named in X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	route, ok := h.routes[op]
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", "unknown operation: "+op)
		return
	}

	route(w, r)
}

// buildRoutes is the operation table: one entry per supported operation.
func (h *Handler) buildRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"CreateFirewall":                         h.createFirewall,
		"DescribeFirewall":                       h.describeFirewall,
		"DeleteFirewall":                         h.deleteFirewall,
		"ListFirewalls":                          h.listFirewalls,
		"CreateFirewallPolicy":                   h.createFirewallPolicy,
		"DescribeFirewallPolicy":                 h.describeFirewallPolicy,
		"UpdateFirewallPolicy":                   h.updateFirewallPolicy,
		"DeleteFirewallPolicy":                   h.deleteFirewallPolicy,
		"ListFirewallPolicies":                   h.listFirewallPolicies,
		"CreateRuleGroup":                        h.createRuleGroup,
		"DescribeRuleGroup":                      h.describeRuleGroup,
		"UpdateRuleGroup":                        h.updateRuleGroup,
		"DeleteRuleGroup":                        h.deleteRuleGroup,
		"ListRuleGroups":                         h.listRuleGroups,
		"AssociateFirewallPolicy":                h.associateFirewallPolicy,
		"AssociateSubnets":                       h.associateSubnets,
		"DisassociateSubnets":                    h.disassociateSubnets,
		"UpdateFirewallDeleteProtection":         h.updateFirewallDeleteProtection,
		"UpdateLoggingConfiguration":             h.updateLoggingConfiguration,
		"DescribeLoggingConfiguration":           h.describeLoggingConfiguration,
		"TagResource":                            h.tagResource,
		"UntagResource":                          h.untagResource,
		"ListTagsForResource":                    h.listTagsForResource,
		"UpdateFirewallDescription":              h.updateFirewallDescription,
		"UpdateSubnetChangeProtection":           h.updateSubnetChangeProtection,
		"UpdateFirewallPolicyChangeProtection":   h.updateFirewallPolicyChangeProtection,
		"UpdateAvailabilityZoneChangeProtection": h.updateAvailabilityZoneChangeProtection,
		"UpdateFirewallAnalysisSettings":         h.updateFirewallAnalysisSettings,
		"AssociateAvailabilityZones":             h.associateAvailabilityZones,
		"DisassociateAvailabilityZones":          h.disassociateAvailabilityZones,
		"PutResourcePolicy":                      h.putResourcePolicy,
		"DescribeResourcePolicy":                 h.describeResourcePolicy,
		"DeleteResourcePolicy":                   h.deleteResourcePolicy,
		"CreateTLSInspectionConfiguration":       h.createTLSInspectionConfiguration,
		"DescribeTLSInspectionConfiguration":     h.describeTLSInspectionConfiguration,
		"UpdateTLSInspectionConfiguration":       h.updateTLSInspectionConfiguration,
		"DeleteTLSInspectionConfiguration":       h.deleteTLSInspectionConfiguration,
		"ListTLSInspectionConfigurations":        h.listTLSInspectionConfigurations,
		"CreateVpcEndpointAssociation":           h.createVpcEndpointAssociation,
		"DescribeVpcEndpointAssociation":         h.describeVpcEndpointAssociation,
		"DeleteVpcEndpointAssociation":           h.deleteVpcEndpointAssociation,
		"ListVpcEndpointAssociations":            h.listVpcEndpointAssociations,
		"DescribeFirewallMetadata":               h.describeFirewallMetadata,
		"DescribeRuleGroupMetadata":              h.describeRuleGroupMetadata,
		"DescribeRuleGroupSummary":               h.describeRuleGroupSummary,
		"StartFlowCapture":                       h.startFlowCapture,
		"StartFlowFlush":                         h.startFlowFlush,
		"DescribeFlowOperation":                  h.describeFlowOperation,
		"ListFlowOperations":                     h.listFlowOperations,
		"ListFlowOperationResults":               h.listFlowOperationResults,
		"StartAnalysisReport":                    h.startAnalysisReport,
		"ListAnalysisReports":                    h.listAnalysisReports,
		"GetAnalysisReportResults":               h.getAnalysisReportResults,
	}
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return "network-firewall" }
