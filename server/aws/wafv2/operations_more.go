package wafv2

import (
	"context"
	"net/http"

	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

func (h *Handler) createRuleGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createRuleGroupRequest) (any, error) {
		grp, err := h.waf.CreateRuleGroup(ctx, wafdriver.CreateRuleGroupInput{
			Name: req.Name, Scope: req.Scope, Description: req.Description,
			Capacity: req.Capacity, VisibilityConfig: req.VisibilityConfig,
			Rules: req.Rules, CustomResponses: req.CustomResponseBodies,
			Tags: tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createSummaryResponse{Summary: ruleGroupSummary(grp)}, nil
	})
}

func (h *Handler) getRuleGroup(w http.ResponseWriter, r *http.Request) {
	getOp(h, w, r, h.waf.GetRuleGroup, func(g *wafdriver.RuleGroup) getRuleGroupResponse {
		return getRuleGroupResponse{RuleGroup: ruleGroupToWire(g), LockToken: g.LockToken}
	})
}

func (h *Handler) updateRuleGroup(w http.ResponseWriter, r *http.Request) {
	updateOp(h, w, r, func(ctx context.Context, req *updateRuleGroupRequest) (string, error) {
		return h.waf.UpdateRuleGroup(ctx, wafdriver.UpdateRuleGroupInput{
			Name: req.Name, Scope: req.Scope, ID: req.ID, LockToken: req.LockToken,
			Description: req.Description, VisibilityConfig: req.VisibilityConfig,
			Rules: req.Rules, CustomResponses: req.CustomResponseBodies,
		})
	})
}

func (h *Handler) deleteRuleGroup(w http.ResponseWriter, r *http.Request) {
	deleteOp(h, w, r, h.waf.DeleteRuleGroup)
}

func (h *Handler) listRuleGroups(w http.ResponseWriter, r *http.Request) {
	listOp(h, w, r, h.waf.ListRuleGroups, ruleGroupSummary, func(s []summaryJSON, marker string) listRuleGroupsResponse {
		return listRuleGroupsResponse{RuleGroups: s, NextMarker: marker}
	})
}

func (h *Handler) createRegexSet(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createRegexSetRequest) (any, error) {
		set, err := h.waf.CreateRegexPatternSet(ctx, wafdriver.CreateRegexPatternSetInput{
			Name: req.Name, Scope: req.Scope, Description: req.Description,
			RegularExpressionList: req.RegularExpressionList, Tags: tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createSummaryResponse{Summary: regexSetSummary(set)}, nil
	})
}

func (h *Handler) getRegexSet(w http.ResponseWriter, r *http.Request) {
	getOp(h, w, r, h.waf.GetRegexPatternSet, func(s *wafdriver.RegexPatternSet) getRegexSetResponse {
		return getRegexSetResponse{RegexPatternSet: regexSetToWire(s), LockToken: s.LockToken}
	})
}

func (h *Handler) updateRegexSet(w http.ResponseWriter, r *http.Request) {
	updateOp(h, w, r, func(ctx context.Context, req *updateRegexSetRequest) (string, error) {
		return h.waf.UpdateRegexPatternSet(ctx, wafdriver.UpdateRegexPatternSetInput{
			Name: req.Name, Scope: req.Scope, ID: req.ID, LockToken: req.LockToken,
			Description: req.Description, RegularExpressionList: req.RegularExpressionList,
		})
	})
}

func (h *Handler) deleteRegexSet(w http.ResponseWriter, r *http.Request) {
	deleteOp(h, w, r, h.waf.DeleteRegexPatternSet)
}

func (h *Handler) listRegexSets(w http.ResponseWriter, r *http.Request) {
	listOp(h, w, r, h.waf.ListRegexPatternSets, regexSetSummary, func(s []summaryJSON, marker string) listRegexSetsResponse {
		return listRegexSetsResponse{RegexPatternSets: s, NextMarker: marker}
	})
}

func (h *Handler) associateWebACL(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *associateRequest) (any, error) {
		if err := h.waf.AssociateWebACL(ctx, req.WebACLArn, req.ResourceArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) disassociateWebACL(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *disassociateRequest) (any, error) {
		if err := h.waf.DisassociateWebACL(ctx, req.ResourceArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) getWebACLForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getForResourceRequest) (any, error) {
		acl, err := h.waf.GetWebACLForResource(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		if acl == nil {
			return getForResourceResponse{}, nil
		}

		wire := webACLToWire(acl)

		return getForResourceResponse{WebACL: &wire}, nil
	})
}

func (h *Handler) listResourcesForWebACL(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listResourcesRequest) (any, error) {
		arns, err := h.waf.ListResourcesForWebACL(ctx, req.WebACLArn, req.ResourceType)
		if err != nil {
			return nil, err
		}

		return listResourcesResponse{ResourceArns: arns}, nil
	})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.waf.TagResource(ctx, req.ResourceARN, tagsToMap(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.waf.UntagResource(ctx, req.ResourceARN, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsRequest) (any, error) {
		arn, tags, err := h.waf.ListTagsForResource(ctx, req.ResourceARN)
		if err != nil {
			return nil, err
		}

		return listTagsResponse{TagInfoForResource: tagInfoJSON{ResourceARN: arn, TagList: mapToTags(tags)}}, nil
	})
}
