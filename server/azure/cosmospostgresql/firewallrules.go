package cosmospostgresql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

func (h *Handler) serveFirewallRules(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	serveCRUD(w, r, rp, crudHandlers{
		put:  h.createOrUpdateFirewallRule,
		get:  h.getFirewallRule,
		del:  h.deleteFirewallRule,
		list: h.listFirewallRules,
	})
}

func (h *Handler) createOrUpdateFirewallRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body firewallRuleResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := cpgdriver.CreateFirewallRuleConfig{
		ResourceGroup: rp.ResourceGroup,
		ClusterName:   rp.ResourceName,
		Name:          rp.SubResourceName,
	}

	if p := body.Properties; p != nil {
		cfg.StartIPAddress = p.StartIPAddress
		cfg.EndIPAddress = p.EndIPAddress
	}

	fr, err := h.db.CreateOrUpdateFirewallRule(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMFirewallRule(fr, h.childID(rp, subFirewallRules, fr.Name)))
}

func (h *Handler) getFirewallRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	fr, err := h.db.GetFirewallRule(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMFirewallRule(fr, h.childID(rp, subFirewallRules, fr.Name)))
}

func (h *Handler) deleteFirewallRule(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteFirewallRule(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listFirewallRules(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rules, err := h.db.ListFirewallRules(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armListOf(rules, func(fr *cpgdriver.FirewallRule) firewallRuleResource {
		return toARMFirewallRule(fr, h.childID(rp, subFirewallRules, fr.Name))
	}))
}
