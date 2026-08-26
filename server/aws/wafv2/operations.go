package wafv2

import (
	"context"
	"net/http"

	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

func (h *Handler) createWebACL(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createWebACLRequest) (any, error) {
		acl, err := h.waf.CreateWebACL(ctx, wafdriver.CreateWebACLInput{
			Name: req.Name, Scope: req.Scope, Description: req.Description,
			DefaultAction: req.DefaultAction, VisibilityConfig: req.VisibilityConfig,
			Rules: req.Rules, TokenDomains: req.TokenDomains,
			CustomResponses: req.CustomResponseBodies,
			CaptchaConfig:   req.CaptchaConfig, ChallengeConfig: req.ChallengeConfig,
			Tags: tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createSummaryResponse{Summary: webACLSummary(acl)}, nil
	})
}

func (h *Handler) getWebACL(w http.ResponseWriter, r *http.Request) {
	getOp(h, w, r, h.waf.GetWebACL, func(a *wafdriver.WebACL) getWebACLResponse {
		return getWebACLResponse{WebACL: webACLToWire(a), LockToken: a.LockToken}
	})
}

func (h *Handler) updateWebACL(w http.ResponseWriter, r *http.Request) {
	updateOp(h, w, r, func(ctx context.Context, req *updateWebACLRequest) (string, error) {
		return h.waf.UpdateWebACL(ctx, wafdriver.UpdateWebACLInput{
			Name: req.Name, Scope: req.Scope, ID: req.ID, LockToken: req.LockToken,
			Description: req.Description, DefaultAction: req.DefaultAction,
			VisibilityConfig: req.VisibilityConfig, Rules: req.Rules,
			TokenDomains: req.TokenDomains, CustomResponses: req.CustomResponseBodies,
			CaptchaConfig: req.CaptchaConfig, ChallengeConfig: req.ChallengeConfig,
		})
	})
}

func (h *Handler) deleteWebACL(w http.ResponseWriter, r *http.Request) {
	deleteOp(h, w, r, h.waf.DeleteWebACL)
}

func (h *Handler) listWebACLs(w http.ResponseWriter, r *http.Request) {
	listOp(h, w, r, h.waf.ListWebACLs, webACLSummary, func(s []summaryJSON, marker string) listWebACLsResponse {
		return listWebACLsResponse{WebACLs: s, NextMarker: marker}
	})
}

func (h *Handler) createIPSet(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createIPSetRequest) (any, error) {
		set, err := h.waf.CreateIPSet(ctx, wafdriver.CreateIPSetInput{
			Name: req.Name, Scope: req.Scope, Description: req.Description,
			IPAddressVersion: req.IPAddressVersion, Addresses: req.Addresses,
			Tags: tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createSummaryResponse{Summary: ipSetSummary(set)}, nil
	})
}

func (h *Handler) getIPSet(w http.ResponseWriter, r *http.Request) {
	getOp(h, w, r, h.waf.GetIPSet, func(s *wafdriver.IPSet) getIPSetResponse {
		return getIPSetResponse{IPSet: ipSetToWire(s), LockToken: s.LockToken}
	})
}

func (h *Handler) updateIPSet(w http.ResponseWriter, r *http.Request) {
	updateOp(h, w, r, func(ctx context.Context, req *updateIPSetRequest) (string, error) {
		return h.waf.UpdateIPSet(ctx, wafdriver.UpdateIPSetInput{
			Name: req.Name, Scope: req.Scope, ID: req.ID, LockToken: req.LockToken,
			Description: req.Description, Addresses: req.Addresses,
		})
	})
}

func (h *Handler) deleteIPSet(w http.ResponseWriter, r *http.Request) {
	deleteOp(h, w, r, h.waf.DeleteIPSet)
}

func (h *Handler) listIPSets(w http.ResponseWriter, r *http.Request) {
	listOp(h, w, r, h.waf.ListIPSets, ipSetSummary, func(s []summaryJSON, marker string) listIPSetsResponse {
		return listIPSetsResponse{IPSets: s, NextMarker: marker}
	})
}
