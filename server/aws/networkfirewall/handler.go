// Package networkfirewall implements the AWS Network Firewall control-plane API
// as a server.Handler. Network Firewall uses AWS JSON 1.0 with the X-Amz-Target
// prefix "NetworkFirewall_20201112.", so real aws-sdk-go-v2 networkfirewall
// clients configured with a custom endpoint hit this handler unchanged.
package networkfirewall

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const targetPrefix = "NetworkFirewall_20201112."

// Handler serves Network Firewall requests against a networkfirewall driver.
type Handler struct {
	db nfdriver.NetworkFirewall
}

// New returns a Network Firewall handler backed by db.
func New(db nfdriver.NetworkFirewall) *Handler {
	return &Handler{db: db}
}

// Matches claims requests whose X-Amz-Target names a Network Firewall operation.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches on the operation named in X-Amz-Target.
//
//nolint:gocyclo // a flat operation switch is the clearest dispatch shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "CreateFirewall":
		h.createFirewall(w, r)
	case "DescribeFirewall":
		h.describeFirewall(w, r)
	case "DeleteFirewall":
		h.deleteFirewall(w, r)
	case "ListFirewalls":
		h.listFirewalls(w, r)
	case "CreateFirewallPolicy":
		h.createFirewallPolicy(w, r)
	case "DescribeFirewallPolicy":
		h.describeFirewallPolicy(w, r)
	case "UpdateFirewallPolicy":
		h.updateFirewallPolicy(w, r)
	case "DeleteFirewallPolicy":
		h.deleteFirewallPolicy(w, r)
	case "ListFirewallPolicies":
		h.listFirewallPolicies(w, r)
	case "CreateRuleGroup":
		h.createRuleGroup(w, r)
	case "DescribeRuleGroup":
		h.describeRuleGroup(w, r)
	case "UpdateRuleGroup":
		h.updateRuleGroup(w, r)
	case "DeleteRuleGroup":
		h.deleteRuleGroup(w, r)
	case "ListRuleGroups":
		h.listRuleGroups(w, r)
	case "AssociateFirewallPolicy":
		h.associateFirewallPolicy(w, r)
	case "AssociateSubnets":
		h.associateSubnets(w, r)
	case "DisassociateSubnets":
		h.disassociateSubnets(w, r)
	case "UpdateFirewallDeleteProtection":
		h.updateFirewallDeleteProtection(w, r)
	case "UpdateLoggingConfiguration":
		h.updateLoggingConfiguration(w, r)
	case "DescribeLoggingConfiguration":
		h.describeLoggingConfiguration(w, r)
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", "unknown operation: "+op)
	}
}

const updateToken = "00000000-0000-0000-0000-000000000000"

func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidOperationException", msg)
	case cerrors.IsInvalidArgument(err), cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", msg)
	}
}
