package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// resolveFirewall returns the firewall's name from a name-or-ARN request (the
// driver mutators are keyed by name), writing the error response on failure.
func (h *Handler) resolveFirewall(w http.ResponseWriter, r *http.Request, name, arn string) (string, bool) {
	if name != "" {
		return name, true
	}

	fw, err := h.db.DescribeFirewall(r.Context(), "", arn)
	if err != nil {
		writeErr(w, err)
		return "", false
	}

	return fw.Name, true
}

type firewallUpdateRequest struct {
	FirewallName string `json:"FirewallName"`
	FirewallArn  string `json:"FirewallArn"`
	UpdateToken  string `json:"UpdateToken"`
}

// base lets the generic firewall-operation helper reach the shared members of
// any request that embeds firewallUpdateRequest.
func (f *firewallUpdateRequest) base() *firewallUpdateRequest { return f }

// firewallRequest is a request body that embeds firewallUpdateRequest.
type firewallRequest[R any] interface {
	*R
	base() *firewallUpdateRequest
}

// firewallCall performs one firewall mutation on a driver capability C for the
// resolved firewall name and returns the updated firewall plus the response
// members specific to the operation.
type firewallCall[C, R any] func(c C, name string, req *R) (*nfdriver.Firewall, map[string]any, error)

// serveFirewallOp decodes the request body into R, resolves the driver
// capability C (UnsupportedOperationException when the driver lacks it) and the
// firewall name, runs call and writes the common response members
// (FirewallArn, FirewallName, UpdateToken) plus the operation-specific ones.
func serveFirewallOp[R any, P firewallRequest[R], C any](h *Handler, w http.ResponseWriter, r *http.Request, call firewallCall[C, R]) {
	var req R
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, ok := capability[C](h, w)
	if !ok {
		return
	}

	base := P(&req).base()

	name, ok := h.resolveFirewall(w, r, base.FirewallName, base.FirewallArn)
	if !ok {
		return
	}

	fw, extra, err := call(c, name, &req)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := map[string]any{"FirewallArn": fw.ARN, "FirewallName": fw.Name, "UpdateToken": fw.UpdateToken}
	for k, v := range extra {
		resp[k] = v
	}

	wire.WriteJSON(w, resp)
}

type associatePolicyRequest struct {
	firewallUpdateRequest
	FirewallPolicyArn string `json:"FirewallPolicyArn"`
}

func (h *Handler) associateFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	serveFirewallOp[associatePolicyRequest](h, w, r,
		func(c nfdriver.NetworkFirewall, name string, req *associatePolicyRequest) (*nfdriver.Firewall, map[string]any, error) {
			fw, err := c.AssociateFirewallPolicy(r.Context(), name, req.FirewallPolicyArn, req.UpdateToken)
			if err != nil {
				return nil, nil, err
			}

			return fw, map[string]any{"FirewallPolicyArn": fw.PolicyARN}, nil
		})
}

type subnetsRequest struct {
	firewallUpdateRequest
	SubnetMappings []subnetMapping `json:"SubnetMappings"`
	SubnetIDs      []string        `json:"SubnetIds"`
}

func (h *Handler) associateSubnets(w http.ResponseWriter, r *http.Request) {
	serveFirewallOp[subnetsRequest](h, w, r,
		func(c nfdriver.NetworkFirewall, name string, req *subnetsRequest) (*nfdriver.Firewall, map[string]any, error) {
			fw, err := c.AssociateSubnets(r.Context(), name, driverSubnets(req.SubnetMappings), req.UpdateToken)
			if err != nil {
				return nil, nil, err
			}

			return fw, map[string]any{"SubnetMappings": subnetMappingsOf(fw)}, nil
		})
}

func (h *Handler) disassociateSubnets(w http.ResponseWriter, r *http.Request) {
	serveFirewallOp[subnetsRequest](h, w, r,
		func(c nfdriver.NetworkFirewall, name string, req *subnetsRequest) (*nfdriver.Firewall, map[string]any, error) {
			fw, err := c.DisassociateSubnets(r.Context(), name, req.SubnetIDs, req.UpdateToken)
			if err != nil {
				return nil, nil, err
			}

			return fw, map[string]any{"SubnetMappings": subnetMappingsOf(fw)}, nil
		})
}

func subnetMappingsOf(fw *nfdriver.Firewall) []subnetMapping {
	mappings := make([]subnetMapping, 0, len(fw.SubnetIDs))
	for _, s := range fw.SubnetIDs {
		mappings = append(mappings, subnetMapping{SubnetID: s, IPAddressType: fw.SubnetDetails[s].IPAddressType})
	}

	return mappings
}

type deleteProtectionRequest struct {
	firewallUpdateRequest
	DeleteProtection bool `json:"DeleteProtection"`
}

func (h *Handler) updateFirewallDeleteProtection(w http.ResponseWriter, r *http.Request) {
	serveFirewallOp[deleteProtectionRequest](h, w, r,
		func(c nfdriver.NetworkFirewall, name string, req *deleteProtectionRequest) (*nfdriver.Firewall, map[string]any, error) {
			fw, err := c.UpdateFirewallDeleteProtection(r.Context(), name, req.DeleteProtection, req.UpdateToken)
			if err != nil {
				return nil, nil, err
			}

			return fw, map[string]any{"DeleteProtection": fw.DeleteProtection}, nil
		})
}

type tagResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []tag  `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.db.TagResource(r.Context(), req.ResourceArn, tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

type untagResourceRequest struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req untagResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.db.UntagResource(r.Context(), req.ResourceArn, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req listTagsForResourceRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.db.ListTagsForResource(r.Context(), req.ResourceArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"Tags": mapToTags(tags)})
}
