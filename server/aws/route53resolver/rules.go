package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// --- wire shapes ---

type wireTargetAddress struct {
	IP                   string `json:"Ip,omitempty"`
	IPv6                 string `json:"Ipv6,omitempty"`
	Port                 int32  `json:"Port,omitempty"`
	Protocol             string `json:"Protocol,omitempty"`
	ServerNameIndication string `json:"ServerNameIndication,omitempty"`
}

type wireResolverRule struct {
	ID                 string              `json:"Id,omitempty"`
	Arn                string              `json:"Arn,omitempty"`
	CreatorRequestID   string              `json:"CreatorRequestId,omitempty"`
	DomainName         string              `json:"DomainName,omitempty"`
	Name               string              `json:"Name,omitempty"`
	OwnerID            string              `json:"OwnerId,omitempty"`
	ResolverEndpointID string              `json:"ResolverEndpointId,omitempty"`
	RuleType           string              `json:"RuleType,omitempty"`
	ShareStatus        string              `json:"ShareStatus,omitempty"`
	Status             string              `json:"Status,omitempty"`
	StatusMessage      string              `json:"StatusMessage,omitempty"`
	TargetIPs          []wireTargetAddress `json:"TargetIps,omitempty"`
	CreationTime       string              `json:"CreationTime,omitempty"`
	ModificationTime   string              `json:"ModificationTime,omitempty"`
}

type wireRuleAssociation struct {
	ID             string `json:"Id,omitempty"`
	Name           string `json:"Name,omitempty"`
	ResolverRuleID string `json:"ResolverRuleId,omitempty"`
	VPCID          string `json:"VPCId,omitempty"`
	Status         string `json:"Status,omitempty"`
	StatusMessage  string `json:"StatusMessage,omitempty"`
}

type wireResolverRuleConfig struct {
	Name               string              `json:"Name"`
	ResolverEndpointID string              `json:"ResolverEndpointId"`
	TargetIPs          []wireTargetAddress `json:"TargetIps"`
}

// --- mapping ---

func targetsToWire(ts []driver.TargetAddress) []wireTargetAddress {
	out := make([]wireTargetAddress, 0, len(ts))
	for _, t := range ts {
		out = append(out, wireTargetAddress{
			IP: t.IP, IPv6: t.IPv6, Port: t.Port,
			Protocol: t.Protocol, ServerNameIndication: t.ServerNameIndication,
		})
	}

	return out
}

func toDriverTargets(ts []wireTargetAddress) []driver.TargetAddress {
	out := make([]driver.TargetAddress, 0, len(ts))
	for _, t := range ts {
		out = append(out, driver.TargetAddress{
			IP: t.IP, IPv6: t.IPv6, Port: t.Port,
			Protocol: t.Protocol, ServerNameIndication: t.ServerNameIndication,
		})
	}

	return out
}

func ruleToWire(r *driver.ResolverRule) wireResolverRule {
	return wireResolverRule{
		ID:                 r.ID,
		Arn:                r.ARN,
		CreatorRequestID:   r.CreatorRequestID,
		DomainName:         r.DomainName,
		Name:               r.Name,
		OwnerID:            r.OwnerID,
		ResolverEndpointID: r.ResolverEndpointID,
		RuleType:           r.RuleType,
		ShareStatus:        r.ShareStatus,
		Status:             r.Status,
		StatusMessage:      r.StatusMessage,
		TargetIPs:          targetsToWire(r.TargetIPs),
		CreationTime:       r.CreatedAt,
		ModificationTime:   r.ModifiedAt,
	}
}

func assocToWire(a *driver.ResolverRuleAssociation) wireRuleAssociation {
	return wireRuleAssociation{
		ID:             a.ID,
		Name:           a.Name,
		ResolverRuleID: a.ResolverRuleID,
		VPCID:          a.VPCID,
		Status:         a.Status,
		StatusMessage:  a.StatusMessage,
	}
}

// --- handlers ---

func (h *Handler) createResolverRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID   string              `json:"CreatorRequestId"`
		Name               string              `json:"Name"`
		RuleType           string              `json:"RuleType"`
		DomainName         string              `json:"DomainName"`
		ResolverEndpointID string              `json:"ResolverEndpointId"`
		TargetIPs          []wireTargetAddress `json:"TargetIps"`
		Tags               []wireTag           `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.r53r.CreateResolverRule(r.Context(), &driver.CreateResolverRuleInput{
		CreatorRequestID:   req.CreatorRequestID,
		Name:               req.Name,
		RuleType:           req.RuleType,
		DomainName:         req.DomainName,
		ResolverEndpointID: req.ResolverEndpointID,
		TargetIPs:          toDriverTargets(req.TargetIPs),
		Tags:               toDriverTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRule": ruleToWire(rule)})
}

func (h *Handler) getResolverRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverRuleID string `json:"ResolverRuleId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.r53r.GetResolverRule(r.Context(), req.ResolverRuleID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRule": ruleToWire(rule)})
}

func (h *Handler) updateResolverRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverRuleID string                 `json:"ResolverRuleId"`
		Config         wireResolverRuleConfig `json:"Config"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.r53r.UpdateResolverRule(r.Context(), req.ResolverRuleID, driver.UpdateResolverRuleInput{
		Name:               req.Config.Name,
		ResolverEndpointID: req.Config.ResolverEndpointID,
		TargetIPs:          toDriverTargets(req.Config.TargetIPs),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRule": ruleToWire(rule)})
}

func (h *Handler) deleteResolverRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverRuleID string `json:"ResolverRuleId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.r53r.DeleteResolverRule(r.Context(), req.ResolverRuleID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRule": ruleToWire(rule)})
}

func (h *Handler) listResolverRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.r53r.ListResolverRules(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireResolverRule, 0, len(rules))
	for i := range rules {
		out = append(out, ruleToWire(&rules[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverRules": out})
}

func (h *Handler) associateResolverRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverRuleID string `json:"ResolverRuleId"`
		VPCID          string `json:"VPCId"`
		Name           string `json:"Name"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.AssociateResolverRule(r.Context(), req.ResolverRuleID, req.VPCID, req.Name)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRuleAssociation": assocToWire(a)})
}

func (h *Handler) disassociateResolverRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverRuleID string `json:"ResolverRuleId"`
		VPCID          string `json:"VPCId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.DisassociateResolverRule(r.Context(), req.ResolverRuleID, req.VPCID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRuleAssociation": assocToWire(a)})
}

func (h *Handler) getResolverRuleAssociation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResolverRuleAssociationID string `json:"ResolverRuleAssociationId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.GetResolverRuleAssociation(r.Context(), req.ResolverRuleAssociationID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRuleAssociation": assocToWire(a)})
}

func (h *Handler) listResolverRuleAssociations(w http.ResponseWriter, r *http.Request) {
	assocs, err := h.r53r.ListResolverRuleAssociations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireRuleAssociation, 0, len(assocs))
	for i := range assocs {
		out = append(out, assocToWire(&assocs[i]))
	}

	wire.WriteJSON(w, map[string]any{"ResolverRuleAssociations": out})
}

func (h *Handler) putResolverRulePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Arn                string `json:"Arn"`
		ResolverRulePolicy string `json:"ResolverRulePolicy"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.r53r.PutResolverRulePolicy(r.Context(), req.Arn, req.ResolverRulePolicy); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ReturnValue": true})
}

func (h *Handler) getResolverRulePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Arn string `json:"Arn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := h.r53r.GetResolverRulePolicy(r.Context(), req.Arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ResolverRulePolicy": policy})
}
