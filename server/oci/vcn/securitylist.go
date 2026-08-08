package vcn

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) securityListOps() crud {
	return crud{
		create: h.createSecurityList,
		list:   h.listSecurityLists,
		get:    h.getSecurityList,
		update: h.updateSecurityList,
		remove: h.deleteSecurityList,
	}
}

func (h *Handler) createSecurityList(w http.ResponseWriter, r *http.Request) {
	var req securityListRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.net.CreateNetworkACL(r.Context(), req.VCNID,
		withInternal(req.FreeformTags, tagDisplayName, req.DisplayName))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	rules := securityListRules(&req)
	if err := h.extras.ReplaceNetworkACLRules(r.Context(), info.ID, rules); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	info.Rules = rules

	ocirest.WriteJSON(w, r, http.StatusOK, h.toSecurityListResponse(info))
}

func (h *Handler) listSecurityLists(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.net.DescribeNetworkACLs(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *netdriver.NetworkACL) (string, string) { return v.ID, v.VPCID },
		h.toSecurityListResponse)
}

func (h *Handler) getSecurityList(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findSecurityList(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toSecurityListResponse(info))
}

// updateSecurityList replaces the whole rule set, as OCI's update does.
func (h *Handler) updateSecurityList(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findSecurityList(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var req securityListRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := updatedTags(info.Tags, req.FreeformTags, tagDisplayName, req.DisplayName)

	if err := h.extras.SetTags(id, tags); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if req.IngressSecurityRules != nil || req.EgressSecurityRules != nil {
		info.Rules = securityListRules(&req)

		if err := h.extras.ReplaceNetworkACLRules(r.Context(), id, info.Rules); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	info.Tags = tags

	ocirest.WriteJSON(w, r, http.StatusOK, h.toSecurityListResponse(info))
}

func (h *Handler) deleteSecurityList(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.net.DeleteNetworkACL(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// securityListRules flattens OCI's two rule arrays into the portable ACL's
// single one. A security list has no deny rules, so every rule is an allow and
// its rule number is its position in its direction.
func securityListRules(req *securityListRequest) []netdriver.NetworkACLRule {
	out := make([]netdriver.NetworkACLRule, 0, len(req.IngressSecurityRules)+len(req.EgressSecurityRules))

	for i := range req.IngressSecurityRules {
		out = append(out, toACLRule(&req.IngressSecurityRules[i], i+1, false))
	}

	for i := range req.EgressSecurityRules {
		out = append(out, toACLRule(&req.EgressSecurityRules[i], i+1, true))
	}

	return out
}

func toACLRule(r *securityRule, number int, egress bool) netdriver.NetworkACLRule {
	rule := toDriverRule(r, egress)

	return netdriver.NetworkACLRule{
		RuleNumber: number,
		Protocol:   rule.Protocol,
		Action:     actionAllow,
		CIDR:       rule.CIDR,
		FromPort:   rule.FromPort,
		ToPort:     rule.ToPort,
		Egress:     egress,
	}
}

// findSecurityList reads one security list, reporting OCI's not-found for an
// unknown OCID.
func (h *Handler) findSecurityList(ctx context.Context, id string) (*netdriver.NetworkACL, error) {
	infos, err := h.net.DescribeNetworkACLs(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "securityList %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toSecurityListResponse(info *netdriver.NetworkACL) securityListResponse {
	ingress := make([]securityRule, 0, len(info.Rules))
	egress := make([]securityRule, 0, len(info.Rules))

	for i := range info.Rules {
		rule := netdriver.SecurityRule{
			Protocol: info.Rules[i].Protocol,
			CIDR:     info.Rules[i].CIDR,
			FromPort: info.Rules[i].FromPort,
			ToPort:   info.Rules[i].ToPort,
		}

		wire := toWireRule(&rule, info.Rules[i].Egress)
		wire.Direction = ""

		if info.Rules[i].Egress {
			egress = append(egress, wire)
		} else {
			ingress = append(ingress, wire)
		}
	}

	return securityListResponse{
		ID:                   info.ID,
		CompartmentID:        h.compartmentOf(info.ID),
		VCNID:                info.VPCID,
		DisplayName:          tagOr(info.Tags, tagDisplayName, ""),
		IngressSecurityRules: ingress,
		EgressSecurityRules:  egress,
		LifecycleState:       lifecycleAvailable,
		TimeCreated:          h.extras.Created(info.ID),
		FreeformTags:         freeformOf(info.Tags),
		DefinedTags:          definedTags{},
	}
}
