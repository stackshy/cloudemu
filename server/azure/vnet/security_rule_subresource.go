package vnet

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Real ARM accepts a security-rule priority only in this range and rejects a
// duplicate priority within the same direction (both documented in "Azure
// network security groups overview", learn.microsoft.com/azure/virtual-network/
// network-security-groups-overview: "A number between 100 and 4096 ... You
// can't create two security rules with the same priority and direction.").
const (
	minSecurityRulePriority = 100
	maxSecurityRulePriority = 4096
)

// routeSecurityRule serves the SecurityRulesClient / azurerm_network_security_rule
// sub-resource surface: PUT/GET/DELETE .../networkSecurityGroups/{nsg}/securityRules/{rule}
// and GET .../networkSecurityGroups/{nsg}/securityRules (list). Registered
// from routeNSG before any whole-NSG handler ever sees the request, so a
// standalone rule op mutates only the addressed rule and preserves siblings.
//
//nolint:gocritic,dupl // rp is request-scoped; mirrors routeVNetPeering over a distinct sub-resource by design
func (h *Handler) routeSecurityRule(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listSecurityRules(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putSecurityRule(w, r, rp)
	case http.MethodGet:
		h.getSecurityRule(w, r, rp)
	case http.MethodDelete:
		h.deleteSecurityRule(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listSecurityRules(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	nsgID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNSG, rp.ResourceName)

	rules := h.customRules(r.Context(), info.ID)

	azurearm.WriteJSON(w, http.StatusOK, securityRuleListResponse{Value: fromAzureNSGRules(nsgID, rules)})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getSecurityRule(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	nsgID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNSG, rp.ResourceName)

	for _, rule := range h.customRules(r.Context(), info.ID) {
		if rule.Name == rp.SubResourceName {
			azurearm.WriteJSON(w, http.StatusOK, fromAzureNSGRules(nsgID, []netdriver.AzureNSGRule{rule})[0])
			return
		}
	}

	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "security rule "+rp.SubResourceName+" not found")
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) putSecurityRule(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"network security groups are not supported by this networking driver")

		return
	}

	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var body securityRule

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	// The URL name is authoritative, matching ARM's PUT-by-name semantics.
	body.Name = rp.SubResourceName

	rule := toAzureNSGRules([]securityRule{body})[0]

	if verr := validateSecurityRule(rule, h.customRules(r.Context(), info.ID)); verr != nil {
		azurearm.WriteCErr(w, verr)
		return
	}

	updated, err := meta.UpsertAzureNSGRule(r.Context(), info.ID, rule)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	nsgID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNSG, rp.ResourceName)

	for _, ru := range updated.SecurityRules {
		if ru.Name == rp.SubResourceName {
			azurearm.WriteJSON(w, http.StatusOK, fromAzureNSGRules(nsgID, []netdriver.AzureNSGRule{ru})[0])
			return
		}
	}

	azurearm.WriteError(w, http.StatusInternalServerError, "InternalError", "security rule not found after create")
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteSecurityRule(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	meta, ok := h.azureMeta()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"network security groups are not supported by this networking driver")

		return
	}

	info, err := findNSGByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := meta.DeleteAzureNSGRule(r.Context(), info.ID, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// customRules returns the caller-defined security rules stored for the NSG
// with the given driver id (empty when the driver carries no Azure metadata
// or the NSG has none yet).
func (h *Handler) customRules(ctx context.Context, nsgDriverID string) []netdriver.AzureNSGRule {
	meta, ok := h.azureMeta()
	if !ok {
		return nil
	}

	md, found := meta.GetAzureNSGMetadata(ctx, nsgDriverID)
	if !found {
		return nil
	}

	return md.SecurityRules
}

// validateSecurityRuleBatch applies validateSecurityRule to every rule in a
// whole-NSG PUT body against its siblings in the same body — the
// createNSG/whole-NSG-replace counterpart of putSecurityRule's single-rule
// check against the already-stored rules.
func validateSecurityRuleBatch(rules []netdriver.AzureNSGRule) error {
	for i := range rules {
		siblings := make([]netdriver.AzureNSGRule, 0, len(rules)-1)

		for j := range rules {
			if j != i {
				siblings = append(siblings, rules[j])
			}
		}

		if err := validateSecurityRule(rules[i], siblings); err != nil {
			return err
		}
	}

	return nil
}

// validateSecurityRule checks a single rule the way real ARM does before ever
// storing it: priority in [100, 4096], no duplicate priority with another
// rule in the same direction, and no name collision with one of the six
// reserved default-rule names (a custom rule can never shadow a default one).
//
//nolint:gocritic // hugeParam: rule is a small value type passed by every caller in this file; a pointer buys nothing here.
func validateSecurityRule(rule netdriver.AzureNSGRule, siblings []netdriver.AzureNSGRule) error {
	if rule.Priority < minSecurityRulePriority || rule.Priority > maxSecurityRulePriority {
		return cerrors.Newf(cerrors.InvalidArgument,
			"priority %d is out of range: must be between %d and %d", rule.Priority, minSecurityRulePriority, maxSecurityRulePriority)
	}

	for i := range azureDefaultRules {
		if rule.Name == azureDefaultRules[i].name {
			return cerrors.Newf(cerrors.InvalidArgument,
				"security rule name %q conflicts with a reserved default security rule", rule.Name)
		}
	}

	for i := range siblings {
		sib := &siblings[i]
		if sib.Name == rule.Name {
			continue
		}

		if sib.Direction == rule.Direction && sib.Priority == rule.Priority {
			return cerrors.Newf(cerrors.InvalidArgument,
				"priority %d is already used by security rule %q in the %s direction", rule.Priority, sib.Name, rule.Direction)
		}
	}

	return nil
}
