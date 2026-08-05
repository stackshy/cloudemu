package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// --- wire shapes ---

type wireFWDomainList struct {
	ID               string `json:"Id,omitempty"`
	Arn              string `json:"Arn,omitempty"`
	Name             string `json:"Name,omitempty"`
	CreatorRequestID string `json:"CreatorRequestId,omitempty"`
	Category         string `json:"Category,omitempty"`
	ManagedOwnerName string `json:"ManagedOwnerName,omitempty"`
	DomainCount      int32  `json:"DomainCount"`
	Status           string `json:"Status,omitempty"`
	StatusMessage    string `json:"StatusMessage,omitempty"`
	CreationTime     string `json:"CreationTime,omitempty"`
	ModificationTime string `json:"ModificationTime,omitempty"`
}

type wireFWRule struct {
	FirewallRuleGroupID             string `json:"FirewallRuleGroupId,omitempty"`
	FirewallDomainListID            string `json:"FirewallDomainListId,omitempty"`
	Name                            string `json:"Name,omitempty"`
	Priority                        int32  `json:"Priority"`
	Action                          string `json:"Action,omitempty"`
	BlockResponse                   string `json:"BlockResponse,omitempty"`
	BlockOverrideDomain             string `json:"BlockOverrideDomain,omitempty"`
	BlockOverrideDNSType            string `json:"BlockOverrideDnsType,omitempty"`
	BlockOverrideTTL                int32  `json:"BlockOverrideTtl,omitempty"`
	Qtype                           string `json:"Qtype,omitempty"`
	ConfidenceThreshold             string `json:"ConfidenceThreshold,omitempty"`
	DNSThreatProtection             string `json:"DnsThreatProtection,omitempty"`
	FirewallDomainRedirectionAction string `json:"FirewallDomainRedirectionAction,omitempty"`
	CreatorRequestID                string `json:"CreatorRequestId,omitempty"`
	Status                          string `json:"Status,omitempty"`
	StatusMessage                   string `json:"StatusMessage,omitempty"`
	CreationTime                    string `json:"CreationTime,omitempty"`
	ModificationTime                string `json:"ModificationTime,omitempty"`
}

type wireFWRuleGroup struct {
	ID               string `json:"Id,omitempty"`
	Arn              string `json:"Arn,omitempty"`
	Name             string `json:"Name,omitempty"`
	CreatorRequestID string `json:"CreatorRequestId,omitempty"`
	OwnerID          string `json:"OwnerId,omitempty"`
	RuleCount        int32  `json:"RuleCount"`
	ShareStatus      string `json:"ShareStatus,omitempty"`
	Status           string `json:"Status,omitempty"`
	StatusMessage    string `json:"StatusMessage,omitempty"`
	CreationTime     string `json:"CreationTime,omitempty"`
	ModificationTime string `json:"ModificationTime,omitempty"`
}

type wireFWAssoc struct {
	ID                  string `json:"Id,omitempty"`
	Arn                 string `json:"Arn,omitempty"`
	Name                string `json:"Name,omitempty"`
	CreatorRequestID    string `json:"CreatorRequestId,omitempty"`
	FirewallRuleGroupID string `json:"FirewallRuleGroupId,omitempty"`
	VPCID               string `json:"VpcId,omitempty"`
	Priority            int32  `json:"Priority"`
	MutationProtection  string `json:"MutationProtection,omitempty"`
	ManagedOwnerName    string `json:"ManagedOwnerName,omitempty"`
	Status              string `json:"Status,omitempty"`
	StatusMessage       string `json:"StatusMessage,omitempty"`
	CreationTime        string `json:"CreationTime,omitempty"`
	ModificationTime    string `json:"ModificationTime,omitempty"`
}

type wireFWConfig struct {
	ID               string `json:"Id,omitempty"`
	OwnerID          string `json:"OwnerId,omitempty"`
	ResourceID       string `json:"ResourceId,omitempty"`
	FirewallFailOpen string `json:"FirewallFailOpen,omitempty"`
}

// --- mapping ---

func fwDomainListToWire(d *driver.FirewallDomainList) wireFWDomainList {
	return wireFWDomainList{
		ID: d.ID, Arn: d.ARN, Name: d.Name, CreatorRequestID: d.CreatorRequestID,
		Category: d.Category, ManagedOwnerName: d.ManagedOwnerName, DomainCount: d.DomainCount,
		Status: d.Status, StatusMessage: d.StatusMessage,
		CreationTime: d.CreatedAt, ModificationTime: d.ModifiedAt,
	}
}

func fwRuleToWire(r *driver.FirewallRule) wireFWRule {
	w := wireFWRule{}
	w.FirewallRuleGroupID = r.FirewallRuleGroupID
	w.FirewallDomainListID = r.FirewallDomainListID
	w.Name, w.Priority, w.Action = r.Name, r.Priority, r.Action
	w.BlockResponse = r.BlockResponse
	w.BlockOverrideDomain = r.BlockOverrideDomain
	w.BlockOverrideDNSType = r.BlockOverrideDNSType
	w.BlockOverrideTTL, w.Qtype = r.BlockOverrideTTL, r.Qtype
	w.ConfidenceThreshold = r.ConfidenceThreshold
	w.DNSThreatProtection = r.DNSThreatProtection
	w.FirewallDomainRedirectionAction = r.FirewallDomainRedirectionAction
	w.CreatorRequestID = r.CreatorRequestID
	w.Status, w.StatusMessage = r.Status, r.StatusMessage
	w.CreationTime, w.ModificationTime = r.CreatedAt, r.ModifiedAt

	return w
}

func fwRuleGroupToWire(g *driver.FirewallRuleGroup) wireFWRuleGroup {
	return wireFWRuleGroup{
		ID: g.ID, Arn: g.ARN, Name: g.Name, CreatorRequestID: g.CreatorRequestID, OwnerID: g.OwnerID,
		RuleCount: g.RuleCount, ShareStatus: g.ShareStatus, Status: g.Status, StatusMessage: g.StatusMessage,
		CreationTime: g.CreatedAt, ModificationTime: g.ModifiedAt,
	}
}

func fwAssocToWire(a *driver.FirewallRuleGroupAssociation) wireFWAssoc {
	return wireFWAssoc{
		ID: a.ID, Arn: a.ARN, Name: a.Name, CreatorRequestID: a.CreatorRequestID,
		FirewallRuleGroupID: a.FirewallRuleGroupID, VPCID: a.VPCID, Priority: a.Priority,
		MutationProtection: a.MutationProtection, ManagedOwnerName: a.ManagedOwnerName,
		Status: a.Status, StatusMessage: a.StatusMessage,
		CreationTime: a.CreatedAt, ModificationTime: a.ModifiedAt,
	}
}

func fwConfigToWire(c *driver.FirewallConfig) wireFWConfig {
	return wireFWConfig{ID: c.ID, OwnerID: c.OwnerID, ResourceID: c.ResourceID, FirewallFailOpen: c.FirewallFailOpen}
}

// wireFWRuleEntry is the create/update entry shape shared by single and batch ops.
type wireFWRuleEntry struct {
	FirewallRuleGroupID             string `json:"FirewallRuleGroupId"`
	FirewallDomainListID            string `json:"FirewallDomainListId"`
	Name                            string `json:"Name"`
	Priority                        int32  `json:"Priority"`
	Action                          string `json:"Action"`
	BlockResponse                   string `json:"BlockResponse"`
	BlockOverrideDomain             string `json:"BlockOverrideDomain"`
	BlockOverrideDNSType            string `json:"BlockOverrideDnsType"`
	BlockOverrideTTL                int32  `json:"BlockOverrideTtl"`
	Qtype                           string `json:"Qtype"`
	ConfidenceThreshold             string `json:"ConfidenceThreshold"`
	DNSThreatProtection             string `json:"DnsThreatProtection"`
	FirewallDomainRedirectionAction string `json:"FirewallDomainRedirectionAction"`
	CreatorRequestID                string `json:"CreatorRequestId"`
}

func (e *wireFWRuleEntry) toInput() driver.FirewallRuleInput {
	return driver.FirewallRuleInput{
		FirewallRuleGroupID: e.FirewallRuleGroupID, FirewallDomainListID: e.FirewallDomainListID,
		Name: e.Name, Priority: e.Priority, Action: e.Action, BlockResponse: e.BlockResponse,
		BlockOverrideDomain: e.BlockOverrideDomain, BlockOverrideDNSType: e.BlockOverrideDNSType,
		BlockOverrideTTL: e.BlockOverrideTTL, Qtype: e.Qtype, ConfidenceThreshold: e.ConfidenceThreshold,
		DNSThreatProtection: e.DNSThreatProtection, FirewallDomainRedirectionAction: e.FirewallDomainRedirectionAction,
		CreatorRequestID: e.CreatorRequestID,
	}
}

// --- domain-list handlers ---

func (h *Handler) createFirewallDomainList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID string    `json:"CreatorRequestId"`
		Name             string    `json:"Name"`
		Tags             []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := h.r53r.CreateFirewallDomainList(r.Context(), req.CreatorRequestID, req.Name, toDriverTags(req.Tags))
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallDomainList": fwDomainListToWire(d)})
}

func (h *Handler) getFirewallDomainList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallDomainListID string `json:"FirewallDomainListId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := h.r53r.GetFirewallDomainList(r.Context(), req.FirewallDomainListID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallDomainList": fwDomainListToWire(d)})
}

func (h *Handler) deleteFirewallDomainList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallDomainListID string `json:"FirewallDomainListId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := h.r53r.DeleteFirewallDomainList(r.Context(), req.FirewallDomainListID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallDomainList": fwDomainListToWire(d)})
}

func (h *Handler) listFirewallDomainLists(w http.ResponseWriter, r *http.Request) {
	ds, err := h.r53r.ListFirewallDomainLists(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireFWDomainList, 0, len(ds))
	for i := range ds {
		out = append(out, fwDomainListToWire(&ds[i]))
	}

	wire.WriteJSON(w, map[string]any{"FirewallDomainLists": out})
}

func (h *Handler) updateFirewallDomains(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallDomainListID string   `json:"FirewallDomainListId"`
		Operation            string   `json:"Operation"`
		Domains              []string `json:"Domains"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := h.r53r.UpdateFirewallDomains(r.Context(), req.FirewallDomainListID, req.Operation, req.Domains)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"Id": d.ID, "Name": d.Name, "Status": d.Status, "StatusMessage": d.StatusMessage,
	})
}

func (h *Handler) importFirewallDomains(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallDomainListID string `json:"FirewallDomainListId"`
		Operation            string `json:"Operation"`
		DomainFileURL        string `json:"DomainFileUrl"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := h.r53r.ImportFirewallDomains(r.Context(), req.FirewallDomainListID, req.Operation, req.DomainFileURL)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"Id": d.ID, "Name": d.Name, "Status": d.Status, "StatusMessage": d.StatusMessage,
	})
}

func (h *Handler) listFirewallDomains(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallDomainListID string `json:"FirewallDomainListId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	domains, err := h.r53r.ListFirewallDomains(r.Context(), req.FirewallDomainListID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Domains": domains})
}

// --- rule handlers ---

func (h *Handler) createFirewallRule(w http.ResponseWriter, r *http.Request) {
	var req wireFWRuleEntry
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := req.toInput()

	rule, err := h.r53r.CreateFirewallRule(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRule": fwRuleToWire(rule)})
}

func (h *Handler) updateFirewallRule(w http.ResponseWriter, r *http.Request) {
	var req wireFWRuleEntry
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := req.toInput()

	rule, err := h.r53r.UpdateFirewallRule(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRule": fwRuleToWire(rule)})
}

func (h *Handler) deleteFirewallRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupID  string `json:"FirewallRuleGroupId"`
		FirewallDomainListID string `json:"FirewallDomainListId"`
		Qtype                string `json:"Qtype"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.r53r.DeleteFirewallRule(r.Context(), req.FirewallRuleGroupID, req.FirewallDomainListID, req.Qtype)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRule": fwRuleToWire(rule)})
}

func (h *Handler) listFirewallRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupID string `json:"FirewallRuleGroupId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rules, err := h.r53r.ListFirewallRules(r.Context(), req.FirewallRuleGroupID)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireFWRule, 0, len(rules))
	for i := range rules {
		out = append(out, fwRuleToWire(&rules[i]))
	}

	wire.WriteJSON(w, map[string]any{"FirewallRules": out})
}

func (h *Handler) batchCreateFirewallRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreateFirewallRuleEntries []wireFWRuleEntry `json:"CreateFirewallRuleEntries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rules, err := h.r53r.BatchCreateFirewallRules(r.Context(), entriesToInputs(req.CreateFirewallRuleEntries))
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"CreatedFirewallRules": rulesToWire(rules)})
}

func (h *Handler) batchUpdateFirewallRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UpdateFirewallRuleEntries []wireFWRuleEntry `json:"UpdateFirewallRuleEntries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rules, err := h.r53r.BatchUpdateFirewallRules(r.Context(), entriesToInputs(req.UpdateFirewallRuleEntries))
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"UpdatedFirewallRules": rulesToWire(rules)})
}

func (h *Handler) batchDeleteFirewallRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeleteFirewallRuleEntries []struct {
			FirewallRuleGroupID  string `json:"FirewallRuleGroupId"`
			FirewallDomainListID string `json:"FirewallDomainListId"`
			Qtype                string `json:"Qtype"`
		} `json:"DeleteFirewallRuleEntries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if len(req.DeleteFirewallRuleEntries) == 0 {
		wire.WriteJSON(w, map[string]any{"DeletedFirewallRules": []wireFWRule{}})

		return
	}

	groupID := req.DeleteFirewallRuleEntries[0].FirewallRuleGroupID
	keys := make([]driver.FirewallRuleKey, 0, len(req.DeleteFirewallRuleEntries))

	for _, e := range req.DeleteFirewallRuleEntries {
		keys = append(keys, driver.FirewallRuleKey{FirewallDomainListID: e.FirewallDomainListID, Qtype: e.Qtype})
	}

	rules, err := h.r53r.BatchDeleteFirewallRules(r.Context(), groupID, keys)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"DeletedFirewallRules": rulesToWire(rules)})
}

func entriesToInputs(entries []wireFWRuleEntry) []driver.FirewallRuleInput {
	out := make([]driver.FirewallRuleInput, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].toInput())
	}

	return out
}

func rulesToWire(rules []driver.FirewallRule) []wireFWRule {
	out := make([]wireFWRule, 0, len(rules))
	for i := range rules {
		out = append(out, fwRuleToWire(&rules[i]))
	}

	return out
}

// --- rule-group handlers ---

func (h *Handler) createFirewallRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID string    `json:"CreatorRequestId"`
		Name             string    `json:"Name"`
		Tags             []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	g, err := h.r53r.CreateFirewallRuleGroup(r.Context(), req.CreatorRequestID, req.Name, toDriverTags(req.Tags))
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroup": fwRuleGroupToWire(g)})
}

func (h *Handler) getFirewallRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupID string `json:"FirewallRuleGroupId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	g, err := h.r53r.GetFirewallRuleGroup(r.Context(), req.FirewallRuleGroupID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroup": fwRuleGroupToWire(g)})
}

func (h *Handler) deleteFirewallRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupID string `json:"FirewallRuleGroupId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	g, err := h.r53r.DeleteFirewallRuleGroup(r.Context(), req.FirewallRuleGroupID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroup": fwRuleGroupToWire(g)})
}

func (h *Handler) listFirewallRuleGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := h.r53r.ListFirewallRuleGroups(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireFWRuleGroup, 0, len(gs))
	for i := range gs {
		out = append(out, fwRuleGroupToWire(&gs[i]))
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroups": out})
}

func (h *Handler) putFirewallRuleGroupPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Arn                     string `json:"Arn"`
		FirewallRuleGroupPolicy string `json:"FirewallRuleGroupPolicy"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.r53r.PutFirewallRuleGroupPolicy(r.Context(), req.Arn, req.FirewallRuleGroupPolicy); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ReturnValue": true})
}

func (h *Handler) getFirewallRuleGroupPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Arn string `json:"Arn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := h.r53r.GetFirewallRuleGroupPolicy(r.Context(), req.Arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroupPolicy": policy})
}

// --- association handlers ---

func (h *Handler) associateFirewallRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CreatorRequestID    string    `json:"CreatorRequestId"`
		FirewallRuleGroupID string    `json:"FirewallRuleGroupId"`
		Name                string    `json:"Name"`
		Priority            int32     `json:"Priority"`
		VPCID               string    `json:"VpcId"`
		MutationProtection  string    `json:"MutationProtection"`
		Tags                []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.AssociateFirewallRuleGroup(r.Context(), &driver.AssociateFirewallRuleGroupInput{
		CreatorRequestID: req.CreatorRequestID, FirewallRuleGroupID: req.FirewallRuleGroupID,
		Name: req.Name, Priority: req.Priority, VPCID: req.VPCID,
		MutationProtection: req.MutationProtection, Tags: toDriverTags(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroupAssociation": fwAssocToWire(a)})
}

func (h *Handler) disassociateFirewallRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupAssociationID string `json:"FirewallRuleGroupAssociationId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.DisassociateFirewallRuleGroup(r.Context(), req.FirewallRuleGroupAssociationID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroupAssociation": fwAssocToWire(a)})
}

func (h *Handler) getFirewallRuleGroupAssociation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupAssociationID string `json:"FirewallRuleGroupAssociationId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.GetFirewallRuleGroupAssociation(r.Context(), req.FirewallRuleGroupAssociationID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroupAssociation": fwAssocToWire(a)})
}

func (h *Handler) listFirewallRuleGroupAssociations(w http.ResponseWriter, r *http.Request) {
	as, err := h.r53r.ListFirewallRuleGroupAssociations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireFWAssoc, 0, len(as))
	for i := range as {
		out = append(out, fwAssocToWire(&as[i]))
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroupAssociations": out})
}

func (h *Handler) updateFirewallRuleGroupAssociation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FirewallRuleGroupAssociationID string `json:"FirewallRuleGroupAssociationId"`
		MutationProtection             string `json:"MutationProtection"`
		Name                           string `json:"Name"`
		Priority                       int32  `json:"Priority"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := h.r53r.UpdateFirewallRuleGroupAssociation(r.Context(), &driver.UpdateFirewallRuleGroupAssociationInput{
		ID: req.FirewallRuleGroupAssociationID, MutationProtection: req.MutationProtection,
		Name: req.Name, Priority: req.Priority,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleGroupAssociation": fwAssocToWire(a)})
}

// --- firewall-config handlers ---

func (h *Handler) getFirewallConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceID string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.GetFirewallConfig(r.Context(), req.ResourceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallConfig": fwConfigToWire(c)})
}

func (h *Handler) updateFirewallConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceID       string `json:"ResourceId"`
		FirewallFailOpen string `json:"FirewallFailOpen"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.r53r.UpdateFirewallConfig(r.Context(), req.ResourceID, req.FirewallFailOpen)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallConfig": fwConfigToWire(c)})
}

func (h *Handler) listFirewallConfigs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.r53r.ListFirewallConfigs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]wireFWConfig, 0, len(cs))
	for i := range cs {
		out = append(out, fwConfigToWire(&cs[i]))
	}

	wire.WriteJSON(w, map[string]any{"FirewallConfigs": out})
}

func (h *Handler) listFirewallRuleTypes(w http.ResponseWriter, r *http.Request) {
	if _, err := h.r53r.ListFirewallRuleTypes(r.Context()); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallRuleTypes": []any{}})
}
