package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// serveNSG routes the NSG collection and its rule and VNIC sub-collections.
func (h *Handler) serveNSG(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Sub {
	case subSecurityRules:
		h.listSecurityRules(w, r, rt.ID)
	case subVNICs:
		h.listNSGVNICs(w, r, rt.ID)
	case subActions:
		h.nsgAction(w, r, rt)
	default:
		serveCRUD(w, r, rt, h.nsgOps())
	}
}

func (h *Handler) nsgOps() crud {
	return crud{
		create: h.createNSG,
		list:   h.listNSGs,
		get:    h.getNSG,
		update: h.updateNSG,
		remove: h.deleteNSG,
	}
}

func (h *Handler) createNSG(w http.ResponseWriter, r *http.Request) {
	var req nsgRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.net.CreateSecurityGroup(r.Context(), netdriver.SecurityGroupConfig{
		Name:  req.DisplayName,
		VPCID: req.VCNID,
		Tags:  withInternal(req.FreeformTags, tagDisplayName, req.DisplayName),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	ocirest.WriteJSON(w, r, http.StatusOK, h.toNSGResponse(info))
}

func (h *Handler) listNSGs(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeSecurityGroups(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.SecurityGroupInfo) (string, string) { return v.ID, v.VPCID },
		h.toNSGResponse)
}

func (h *Handler) getNSG(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findNSG(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toNSGResponse(info))
}

func (h *Handler) updateNSG(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findNSG(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req nsgRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := updatedTags(info.Tags, req.FreeformTags, tagDisplayName, req.DisplayName)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toNSGResponse(info))
}

func (h *Handler) deleteNSG(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteSecurityGroup(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// listSecurityRules answers the NSG's securityRules sub-collection.
func (h *Handler) listSecurityRules(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	info, err := h.findNSG(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	rules := wireRules(info)

	if want := r.URL.Query().Get("direction"); want != "" {
		filtered := make([]securityRule, 0, len(rules))

		for i := range rules {
			if rules[i].Direction == want {
				filtered = append(filtered, rules[i])
			}
		}

		rules = filtered
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, rules))
}

// listNSGVNICs answers the NSG's vnics sub-collection.
func (h *Handler) listNSGVNICs(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	vnics, err := h.extras.VNICsInNSG(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]vnicResponse, 0, len(vnics))
	for i := range vnics {
		out = append(out, h.toVNICResponse(&vnics[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

// nsgAction serves the rule mutations, which OCI models as actions rather
// than as writes to the sub-collection.
func (h *Handler) nsgAction(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	switch rt.Action {
	case actionAddRules:
		h.addSecurityRules(w, r, rt.ID)
	case actionRemoveRules:
		h.removeSecurityRules(w, r, rt.ID)
	case actionUpdateRules:
		h.updateSecurityRules(w, r, rt.ID)
	case actionChangeCompartment:
		h.changeCompartment(w, r, rt.ID)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+rt.Action)
	}
}

func (h *Handler) addSecurityRules(w http.ResponseWriter, r *http.Request, id string) {
	var req securityRulesRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	added := make([]securityRule, 0, len(req.SecurityRules))

	for i := range req.SecurityRules {
		rule := req.SecurityRules[i]
		egress := rule.Direction == directionEgress
		driverRule := toDriverRule(&rule, egress)

		if err := h.addRule(r.Context(), id, driverRule, egress); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}

		added = append(added, toWireRule(&driverRule, egress))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, securityRulesResponse{SecurityRules: added})
}

func (h *Handler) removeSecurityRules(w http.ResponseWriter, r *http.Request, id string) {
	var req removeSecurityRulesRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	for _, ruleID := range req.SecurityRuleIDs {
		if err := h.removeRuleByID(r.Context(), id, ruleID); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// updateSecurityRules replaces rules by id, which OCI does in place.
func (h *Handler) updateSecurityRules(w http.ResponseWriter, r *http.Request, id string) {
	var req securityRulesRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	updated := make([]securityRule, 0, len(req.SecurityRules))

	for i := range req.SecurityRules {
		rule := req.SecurityRules[i]

		if err := h.removeRuleByID(r.Context(), id, rule.ID); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}

		egress := rule.Direction == directionEgress
		driverRule := toDriverRule(&rule, egress)

		if err := h.addRule(r.Context(), id, driverRule, egress); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}

		updated = append(updated, toWireRule(&driverRule, egress))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, securityRulesResponse{SecurityRules: updated})
}

// addRule adds a rule on the side its direction names.
func (h *Handler) addRule(ctx context.Context, id string, rule netdriver.SecurityRule, egress bool) error {
	if egress {
		return h.net.AddEgressRule(ctx, id, rule)
	}

	return h.net.AddIngressRule(ctx, id, rule)
}

// removeRuleByID drops the rule whose derived handle matches.
func (h *Handler) removeRuleByID(ctx context.Context, id, wantID string) error {
	info, err := h.findNSG(ctx, id)
	if err != nil {
		return err
	}

	for i := range info.IngressRules {
		if ruleID(directionIngress, &info.IngressRules[i]) == wantID {
			return h.net.RemoveIngressRule(ctx, id, info.IngressRules[i])
		}
	}

	for i := range info.EgressRules {
		if ruleID(directionEgress, &info.EgressRules[i]) == wantID {
			return h.net.RemoveEgressRule(ctx, id, info.EgressRules[i])
		}
	}

	return cerrors.Newf(cerrors.NotFound, "security rule %s not found in %s", wantID, id)
}

// findNSG reads one NSG, reporting OCI's not-found for an unknown OCID.
func (h *Handler) findNSG(ctx context.Context, id string) (*netdriver.SecurityGroupInfo, error) {
	infos, err := h.net.DescribeSecurityGroups(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "networkSecurityGroup %s not found", id)
	}

	return &infos[0], nil
}

// wireRules renders an NSG's rules in OCI's shape, ingress first.
func wireRules(info *netdriver.SecurityGroupInfo) []securityRule {
	out := make([]securityRule, 0, len(info.IngressRules)+len(info.EgressRules))

	for i := range info.IngressRules {
		out = append(out, toWireRule(&info.IngressRules[i], false))
	}

	for i := range info.EgressRules {
		out = append(out, toWireRule(&info.EgressRules[i], true))
	}

	return out
}

func (h *Handler) toNSGResponse(info *netdriver.SecurityGroupInfo) nsgResponse {
	return nsgResponse{
		ID:             info.ID,
		CompartmentID:  h.compartmentOf(info.ID),
		VCNID:          info.VPCID,
		DisplayName:    tagOr(info.Tags, tagDisplayName, info.Name),
		LifecycleState: lifecycleAvailable,
		TimeCreated:    h.extras.Created(info.ID),
		FreeformTags:   freeformOf(info.Tags),
		DefinedTags:    definedTags{},
	}
}
