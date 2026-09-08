package ecr

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func (h *Handler) pullThroughMgr(w http.ResponseWriter) (pullThroughManager, bool) {
	mgr, ok := h.registry.(pullThroughManager)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "pull through cache rules not supported"))
	}

	return mgr, ok
}

func (h *Handler) createPullThroughCacheRule(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.pullThroughMgr(w)
	if !ok {
		return
	}

	var req struct {
		ECRRepositoryPrefix string `json:"ecrRepositoryPrefix"`
		UpstreamRegistryURL string `json:"upstreamRegistryUrl"`
		UpstreamRegistry    string `json:"upstreamRegistry"`
		CredentialARN       string `json:"credentialArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := mgr.CreatePullThroughCacheRule(r.Context(), &crdriver.PullThroughCacheRule{
		ECRRepositoryPrefix: req.ECRRepositoryPrefix,
		UpstreamRegistryURL: req.UpstreamRegistryURL,
		UpstreamRegistry:    req.UpstreamRegistry,
		CredentialARN:       req.CredentialARN,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writePullThroughRule(w, &rule)
}

func (h *Handler) updatePullThroughCacheRule(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.pullThroughMgr(w)
	if !ok {
		return
	}

	var req struct {
		ECRRepositoryPrefix string `json:"ecrRepositoryPrefix"`
		CredentialARN       string `json:"credentialArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := mgr.UpdatePullThroughCacheRule(r.Context(), req.ECRRepositoryPrefix, req.CredentialARN)
	if err != nil {
		writeErr(w, err)
		return
	}

	writePullThroughRule(w, &rule)
}

func (h *Handler) describePullThroughCacheRules(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.pullThroughMgr(w)
	if !ok {
		return
	}

	var req struct {
		ECRRepositoryPrefixes []string `json:"ecrRepositoryPrefixes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rules, registryID, err := mgr.DescribePullThroughCacheRules(r.Context(), req.ECRRepositoryPrefixes)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]pullThroughRuleJSON, 0, len(rules))
	for i := range rules {
		out = append(out, pullThroughToJSON(&rules[i]))
	}

	wire.WriteJSON(w, map[string]any{"pullThroughCacheRules": out, "registryId": registryID})
}

func (h *Handler) deletePullThroughCacheRule(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.pullThroughMgr(w)
	if !ok {
		return
	}

	var req struct {
		ECRRepositoryPrefix string `json:"ecrRepositoryPrefix"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := mgr.DeletePullThroughCacheRule(r.Context(), req.ECRRepositoryPrefix)
	if err != nil {
		writeErr(w, err)
		return
	}

	writePullThroughRule(w, &rule)
}

// writePullThroughRule writes a single pull-through cache rule as the flat wire
// object ECR returns from Create/Update/Delete.
func writePullThroughRule(w http.ResponseWriter, rule *crdriver.PullThroughCacheRule) {
	j := pullThroughToJSON(rule)
	wire.WriteJSON(w, map[string]any{
		"ecrRepositoryPrefix": j.ECRRepositoryPrefix,
		"upstreamRegistryUrl": j.UpstreamRegistryURL,
		"upstreamRegistry":    j.UpstreamRegistry,
		"credentialArn":       j.CredentialARN,
		"registryId":          j.RegistryID,
		"createdAt":           j.CreatedAt,
		"updatedAt":           j.UpdatedAt,
	})
}
