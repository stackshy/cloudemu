package ecr

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// ECR carries a lifecycle policy as a JSON document string (lifecyclePolicyText)
// rather than a structured field. These types model that document so it can be
// mapped to and from the portable driver's LifecyclePolicy.
type lifecyclePolicyText struct {
	Rules []lifecycleRuleText `json:"rules"`
}

type lifecycleRuleText struct {
	RulePriority int                `json:"rulePriority"`
	Description  string             `json:"description,omitempty"`
	Selection    lifecycleSelection `json:"selection"`
	Action       lifecycleAction    `json:"action"`
}

type lifecycleSelection struct {
	TagStatus      string   `json:"tagStatus"`
	TagPatternList []string `json:"tagPatternList,omitempty"`
	TagPrefixList  []string `json:"tagPrefixList,omitempty"`
	CountType      string   `json:"countType"`
	CountNumber    int      `json:"countNumber"`
}

type lifecycleAction struct {
	Type string `json:"type"`
}

func (h *Handler) putLifecyclePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName      string `json:"repositoryName"`
		LifecyclePolicyText string `json:"lifecyclePolicyText"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := parseLifecyclePolicyText(req.LifecyclePolicyText)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
		return
	}

	if err := h.registry.PutLifecyclePolicy(r.Context(), req.RepositoryName, policy); err != nil {
		writeErr(w, err)
		return
	}

	resp := map[string]any{
		"repositoryName":      req.RepositoryName,
		"lifecyclePolicyText": req.LifecyclePolicyText,
	}

	if repo, err := h.registry.GetRepository(r.Context(), req.RepositoryName); err == nil {
		resp["registryId"] = repo.RegistryID
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) getLifecyclePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := h.registry.GetLifecyclePolicy(r.Context(), req.RepositoryName)
	if err != nil {
		// A missing repository is RepositoryNotFoundException; a repository with
		// no lifecycle policy is LifecyclePolicyNotFoundException.
		if _, repoErr := h.registry.GetRepository(r.Context(), req.RepositoryName); repoErr != nil {
			writeErr(w, repoErr)
			return
		}

		wire.WriteJSONError(w, http.StatusBadRequest, "LifecyclePolicyNotFoundException", err.Error())

		return
	}

	text, err := marshalLifecyclePolicyText(policy)
	if err != nil {
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServerException", err.Error())
		return
	}

	resp := map[string]any{
		"repositoryName":      req.RepositoryName,
		"lifecyclePolicyText": text,
	}

	// The policy lookup already proved the repository exists, so echo its
	// registryId (real ECR always returns it).
	if repo, repoErr := h.registry.GetRepository(r.Context(), req.RepositoryName); repoErr == nil {
		resp["registryId"] = repo.RegistryID
	}

	wire.WriteJSON(w, resp)
}

func parseLifecyclePolicyText(text string) (crdriver.LifecyclePolicy, error) {
	var doc lifecyclePolicyText
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return crdriver.LifecyclePolicy{}, err
	}

	rules := make([]crdriver.LifecycleRule, 0, len(doc.Rules))

	for i := range doc.Rules {
		src := &doc.Rules[i]
		rules = append(rules, crdriver.LifecycleRule{
			Priority:    src.RulePriority,
			Description: src.Description,
			TagStatus:   src.Selection.TagStatus,
			TagPattern:  firstTagPattern(&src.Selection),
			CountType:   src.Selection.CountType,
			CountValue:  src.Selection.CountNumber,
			Action:      src.Action.Type,
		})
	}

	return crdriver.LifecyclePolicy{Rules: rules}, nil
}

func firstTagPattern(sel *lifecycleSelection) string {
	if len(sel.TagPatternList) > 0 {
		return sel.TagPatternList[0]
	}

	if len(sel.TagPrefixList) > 0 {
		return sel.TagPrefixList[0]
	}

	return ""
}

func marshalLifecyclePolicyText(policy *crdriver.LifecyclePolicy) (string, error) {
	doc := lifecyclePolicyText{Rules: make([]lifecycleRuleText, 0, len(policy.Rules))}

	for i := range policy.Rules {
		rule := &policy.Rules[i]
		sel := lifecycleSelection{
			TagStatus:   rule.TagStatus,
			CountType:   rule.CountType,
			CountNumber: rule.CountValue,
		}

		if rule.TagPattern != "" {
			sel.TagPatternList = []string{rule.TagPattern}
		}

		doc.Rules = append(doc.Rules, lifecycleRuleText{
			RulePriority: rule.Priority,
			Description:  rule.Description,
			Selection:    sel,
			Action:       lifecycleAction{Type: rule.Action},
		})
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}

	return string(out), nil
}
