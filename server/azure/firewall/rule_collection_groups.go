package firewall

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fwdriver "github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

const (
	subRuleCollectionGroups = "ruleCollectionGroups"
	groupResourceType       = providerName + "/" + typeFirewallPolicies + "/" + subRuleCollectionGroups
	// groupMaxDepth is the deepest group route: {policy}/ruleCollectionGroups/{name}.
	groupMaxDepth = 3
)

type ruleCollectionGroupJSON struct {
	ID         string                    `json:"id,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Type       string                    `json:"type,omitempty"`
	Etag       string                    `json:"etag,omitempty"`
	Properties *ruleCollectionGroupProps `json:"properties,omitempty"`
}

type ruleCollectionGroupList struct {
	Value []ruleCollectionGroupJSON `json:"value"`
}

type ruleCollectionGroupProps struct {
	Priority          int    `json:"priority"`
	RuleCollections   []any  `json:"ruleCollections"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}

// serveRuleCollectionGroups handles .../firewallPolicies/{p}/
// ruleCollectionGroups[/{name}] (FirewallPolicyRuleCollectionGroups
// CreateOrUpdate/Get/List/Delete).
func (h *Handler) serveRuleCollectionGroups(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, groupMaxDepth) {
		return
	}

	ctx := r.Context()

	if rp.SubResourceName == "" {
		h.listRuleCollectionGroups(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putRuleCollectionGroup(w, r, rp)
	case http.MethodGet:
		g, err := h.fw.GetRuleCollectionGroup(ctx, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toGroupJSON(rp, g))
	case http.MethodDelete:
		h.deleteRuleCollectionGroup(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) listRuleCollectionGroups(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	groups, err := h.fw.ListRuleCollectionGroups(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]ruleCollectionGroupJSON, 0, len(groups))
	for i := range groups {
		out = append(out, toGroupJSON(rp, &groups[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, ruleCollectionGroupList{Value: out})
}

// deleteRuleCollectionGroup removes one group; an absent group is 204.
func (h *Handler) deleteRuleCollectionGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	err := h.fw.DeleteRuleCollectionGroup(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)

	switch {
	case cerrors.IsNotFound(err):
		w.WriteHeader(http.StatusNoContent)
	case err != nil:
		azurearm.WriteCErr(w, err)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) putRuleCollectionGroup(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body ruleCollectionGroupJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	g := fwdriver.RuleCollectionGroup{Name: rp.SubResourceName}
	if body.Properties != nil {
		g.Priority = body.Properties.Priority
		g.RuleCollections = body.Properties.RuleCollections
	}

	stored, created, err := h.fw.CreateOrUpdateRuleCollectionGroup(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, g)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toGroupJSON(rp, stored))
}

func toGroupJSON(rp *azurearm.ResourcePath, g *fwdriver.RuleCollectionGroup) ruleCollectionGroupJSON {
	polID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeFirewallPolicies, rp.ResourceName)
	id := polID + "/" + subRuleCollectionGroups + "/" + g.Name

	collections := g.RuleCollections
	if collections == nil {
		collections = []any{}
	}

	return ruleCollectionGroupJSON{
		ID:   id,
		Name: g.Name,
		Type: groupResourceType,
		Etag: azurearm.WeakETag(id),
		Properties: &ruleCollectionGroupProps{
			Priority: g.Priority, RuleCollections: collections, ProvisioningState: "Succeeded",
		},
	}
}

func isRuleCollectionGroups(sub string) bool {
	return strings.EqualFold(sub, subRuleCollectionGroups)
}
