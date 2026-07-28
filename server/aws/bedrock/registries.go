package bedrock

import (
	"encoding/json"
	"net/http"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- inference profile wire types ---

type inferenceProfileModelJSON struct {
	ModelARN string `json:"modelArn,omitempty"`
}

type inferenceProfileModelSourceJSON struct {
	CopyFrom string `json:"copyFrom,omitempty"`
}

type createInferenceProfileRequest struct {
	InferenceProfileName string                           `json:"inferenceProfileName"`
	ModelSource          *inferenceProfileModelSourceJSON `json:"modelSource"`
	ClientRequestToken   string                           `json:"clientRequestToken"`
	Description          string                           `json:"description"`
	Tags                 []tagPair                        `json:"tags"`
}

type createInferenceProfileResponse struct {
	InferenceProfileARN string `json:"inferenceProfileArn"`
	Status              string `json:"status"`
}

type inferenceProfileJSON struct {
	InferenceProfileARN  string                      `json:"inferenceProfileArn"`
	InferenceProfileID   string                      `json:"inferenceProfileId,omitempty"`
	InferenceProfileName string                      `json:"inferenceProfileName,omitempty"`
	Models               []inferenceProfileModelJSON `json:"models,omitempty"`
	Status               string                      `json:"status"`
	Type                 string                      `json:"type"`
	Description          string                      `json:"description,omitempty"`
	CreatedAt            string                      `json:"createdAt,omitempty"`
	UpdatedAt            string                      `json:"updatedAt,omitempty"`
}

type listInferenceProfilesResponse struct {
	InferenceProfileSummaries []inferenceProfileJSON `json:"inferenceProfileSummaries"`
	NextToken                 string                 `json:"nextToken,omitempty"`
}

// --- prompt router wire types ---

type promptRouterTargetModelJSON struct {
	ModelARN string `json:"modelArn,omitempty"`
}

type routingCriteriaJSON struct {
	ResponseQualityDifference *float64 `json:"responseQualityDifference,omitempty"`
}

type createPromptRouterRequest struct {
	PromptRouterName   string                        `json:"promptRouterName"`
	Models             []promptRouterTargetModelJSON `json:"models"`
	RoutingCriteria    *routingCriteriaJSON          `json:"routingCriteria"`
	FallbackModel      *promptRouterTargetModelJSON  `json:"fallbackModel"`
	ClientRequestToken string                        `json:"clientRequestToken"`
	Description        string                        `json:"description"`
	Tags               []tagPair                     `json:"tags"`
}

type createPromptRouterResponse struct {
	PromptRouterARN string `json:"promptRouterArn"`
}

type promptRouterJSON struct {
	PromptRouterARN  string                        `json:"promptRouterArn"`
	PromptRouterName string                        `json:"promptRouterName,omitempty"`
	Models           []promptRouterTargetModelJSON `json:"models,omitempty"`
	RoutingCriteria  *routingCriteriaJSON          `json:"routingCriteria,omitempty"`
	FallbackModel    *promptRouterTargetModelJSON  `json:"fallbackModel,omitempty"`
	Status           string                        `json:"status"`
	Type             string                        `json:"type"`
	Description      string                        `json:"description,omitempty"`
	CreatedAt        string                        `json:"createdAt,omitempty"`
	UpdatedAt        string                        `json:"updatedAt,omitempty"`
}

type listPromptRoutersResponse struct {
	PromptRouterSummaries []promptRouterJSON `json:"promptRouterSummaries"`
	NextToken             string             `json:"nextToken,omitempty"`
}

// --- automated reasoning policy wire types ---

type createARPolicyRequest struct {
	Name               string          `json:"name"`
	ClientRequestToken string          `json:"clientRequestToken"`
	Description        string          `json:"description"`
	KMSKeyID           string          `json:"kmsKeyId"`
	PolicyDefinition   json.RawMessage `json:"policyDefinition"`
	Tags               []tagPair       `json:"tags"`
}

type updateARPolicyRequest struct {
	PolicyDefinition json.RawMessage `json:"policyDefinition"`
	Description      string          `json:"description"`
	Name             string          `json:"name"`
}

type createARPolicyResponse struct {
	PolicyARN      string `json:"policyArn"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
	CreatedAt      string `json:"createdAt,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
	Description    string `json:"description,omitempty"`
}

type arPolicyJSON struct {
	PolicyARN      string `json:"policyArn"`
	PolicyID       string `json:"policyId,omitempty"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
	CreatedAt      string `json:"createdAt,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
	Description    string `json:"description,omitempty"`
	KMSKeyARN      string `json:"kmsKeyArn,omitempty"`
}

type arPolicySummaryJSON struct {
	PolicyARN   string `json:"policyArn"`
	PolicyID    string `json:"policyId,omitempty"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Description string `json:"description,omitempty"`
}

type listARPoliciesResponse struct {
	AutomatedReasoningPolicySummaries []arPolicySummaryJSON `json:"automatedReasoningPolicySummaries"`
	NextToken                         string                `json:"nextToken,omitempty"`
}

type updateARPolicyResponse struct {
	PolicyARN      string `json:"policyArn"`
	Name           string `json:"name"`
	DefinitionHash string `json:"definitionHash"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
}

// --- dispatcher ---

// serveRegistries routes the inference-profile, prompt-router, and
// automated-reasoning-policy control-plane surfaces. Split out of ServeHTTP to
// keep each dispatcher small.
func (h *Handler) serveRegistries(w http.ResponseWriter, r *http.Request, p string) {
	switch {
	case p == prefixInferenceProfiles || strings.HasPrefix(p, prefixInferenceProfiles+"/"):
		h.serveInferenceProfiles(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixInferenceProfiles), "/"))
	case p == prefixPromptRouters || strings.HasPrefix(p, prefixPromptRouters+"/"):
		h.servePromptRouters(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixPromptRouters), "/"))
	case p == prefixARPolicies || strings.HasPrefix(p, prefixARPolicies+"/"):
		h.serveARPolicies(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixARPolicies), "/"))
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+p)
	}
}

// --- inference profile dispatch + operations ---

// serveInferenceProfiles handles /inference-profiles[/{inferenceProfileIdentifier}].
func (h *Handler) serveInferenceProfiles(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createInferenceProfile(w, r)
		case http.MethodGet:
			h.listInferenceProfiles(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getInferenceProfile(w, r, id)
	case http.MethodDelete:
		h.deleteInferenceProfile(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createInferenceProfile(w http.ResponseWriter, r *http.Request) {
	var in createInferenceProfileRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	p, err := h.bedrock.CreateInferenceProfile(r.Context(), bedrockdriver.InferenceProfileConfig{
		Name:                in.InferenceProfileName,
		ModelSourceCopyFrom: modelSourceCopyFrom(in.ModelSource),
		ClientRequestToken:  in.ClientRequestToken,
		Description:         in.Description,
		Tags:                tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createInferenceProfileResponse{InferenceProfileARN: p.ARN, Status: p.Status})
}

func (h *Handler) getInferenceProfile(w http.ResponseWriter, r *http.Request, id string) {
	p, err := h.bedrock.GetInferenceProfile(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toInferenceProfileJSON(p))
}

func (h *Handler) listInferenceProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.bedrock.ListInferenceProfiles(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]inferenceProfileJSON, 0, len(profiles))
	for i := range profiles {
		out = append(out, toInferenceProfileJSON(&profiles[i]))
	}

	writeJSON(w, listInferenceProfilesResponse{InferenceProfileSummaries: out})
}

func (h *Handler) deleteInferenceProfile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.bedrock.DeleteInferenceProfile(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- prompt router dispatch + operations ---

// servePromptRouters handles /prompt-routers[/{promptRouterArn}]. The router ARN
// contains slashes, so it is the entire remainder of the path.
func (h *Handler) servePromptRouters(w http.ResponseWriter, r *http.Request, arn string) {
	if arn == "" {
		switch r.Method {
		case http.MethodPost:
			h.createPromptRouter(w, r)
		case http.MethodGet:
			h.listPromptRouters(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getPromptRouter(w, r, arn)
	case http.MethodDelete:
		h.deletePromptRouter(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createPromptRouter(w http.ResponseWriter, r *http.Request) {
	var in createPromptRouterRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	p, err := h.bedrock.CreatePromptRouter(r.Context(), bedrockdriver.PromptRouterConfig{
		Name:                      in.PromptRouterName,
		Models:                    modelARNsOf(in.Models),
		ResponseQualityDifference: responseQualityDifference(in.RoutingCriteria),
		FallbackModelARN:          fallbackModelARN(in.FallbackModel),
		ClientRequestToken:        in.ClientRequestToken,
		Description:               in.Description,
		Tags:                      tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createPromptRouterResponse{PromptRouterARN: p.ARN})
}

func (h *Handler) getPromptRouter(w http.ResponseWriter, r *http.Request, arn string) {
	p, err := h.bedrock.GetPromptRouter(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toPromptRouterJSON(p))
}

func (h *Handler) listPromptRouters(w http.ResponseWriter, r *http.Request) {
	routers, err := h.bedrock.ListPromptRouters(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]promptRouterJSON, 0, len(routers))
	for i := range routers {
		out = append(out, toPromptRouterJSON(&routers[i]))
	}

	writeJSON(w, listPromptRoutersResponse{PromptRouterSummaries: out})
}

func (h *Handler) deletePromptRouter(w http.ResponseWriter, r *http.Request, arn string) {
	if err := h.bedrock.DeletePromptRouter(r.Context(), arn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- automated reasoning policy dispatch + operations ---

// serveARPolicies handles /automated-reasoning-policies[/{policyArn}]. The policy
// ARN contains slashes, so it is the entire remainder of the path.
func (h *Handler) serveARPolicies(w http.ResponseWriter, r *http.Request, arn string) {
	if arn != "" {
		switch r.Method {
		case http.MethodGet:
			h.getARPolicy(w, r, arn)
		case http.MethodPatch:
			h.updateARPolicy(w, r, arn)
		case http.MethodDelete:
			h.deleteARPolicy(w, r, arn)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.createARPolicy(w, r)
	case http.MethodGet:
		h.listARPolicies(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createARPolicy(w http.ResponseWriter, r *http.Request) {
	var in createARPolicyRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	p, err := h.bedrock.CreateAutomatedReasoningPolicy(r.Context(), bedrockdriver.AutomatedReasoningPolicyConfig{
		Name:               in.Name,
		ClientRequestToken: in.ClientRequestToken,
		Description:        in.Description,
		KMSKeyID:           in.KMSKeyID,
		PolicyDefinition:   []byte(in.PolicyDefinition),
		Tags:               tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createARPolicyResponse{
		PolicyARN:      p.ARN,
		Name:           p.Name,
		Version:        p.Version,
		DefinitionHash: p.DefinitionHash,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
		Description:    p.Description,
	})
}

func (h *Handler) getARPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	p, err := h.bedrock.GetAutomatedReasoningPolicy(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toARPolicyJSON(p))
}

func (h *Handler) listARPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.bedrock.ListAutomatedReasoningPolicies(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]arPolicySummaryJSON, 0, len(policies))
	for i := range policies {
		out = append(out, toARPolicySummaryJSON(&policies[i]))
	}

	writeJSON(w, listARPoliciesResponse{AutomatedReasoningPolicySummaries: out})
}

func (h *Handler) updateARPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	var in updateARPolicyRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	p, err := h.bedrock.UpdateAutomatedReasoningPolicy(r.Context(), arn, bedrockdriver.AutomatedReasoningPolicyUpdate{
		PolicyDefinition: []byte(in.PolicyDefinition),
		Description:      in.Description,
		Name:             in.Name,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateARPolicyResponse{
		PolicyARN:      p.ARN,
		Name:           p.Name,
		DefinitionHash: p.DefinitionHash,
		UpdatedAt:      p.UpdatedAt,
	})
}

func (h *Handler) deleteARPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	if err := h.bedrock.DeleteAutomatedReasoningPolicy(r.Context(), arn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- converters ---

func modelSourceCopyFrom(in *inferenceProfileModelSourceJSON) string {
	if in == nil {
		return ""
	}

	return in.CopyFrom
}

func modelARNsOf(models []promptRouterTargetModelJSON) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ModelARN)
	}

	return out
}

func fallbackModelARN(in *promptRouterTargetModelJSON) string {
	if in == nil {
		return ""
	}

	return in.ModelARN
}

func responseQualityDifference(in *routingCriteriaJSON) *float64 {
	if in == nil {
		return nil
	}

	return in.ResponseQualityDifference
}

func targetModelsOf(arns []string) []promptRouterTargetModelJSON {
	if len(arns) == 0 {
		return nil
	}

	out := make([]promptRouterTargetModelJSON, 0, len(arns))
	for _, a := range arns {
		out = append(out, promptRouterTargetModelJSON{ModelARN: a})
	}

	return out
}

func toInferenceProfileJSON(p *bedrockdriver.InferenceProfile) inferenceProfileJSON {
	models := make([]inferenceProfileModelJSON, 0, len(p.Models))
	for _, a := range p.Models {
		models = append(models, inferenceProfileModelJSON{ModelARN: a})
	}

	return inferenceProfileJSON{
		InferenceProfileARN:  p.ARN,
		InferenceProfileID:   p.ID,
		InferenceProfileName: p.Name,
		Models:               models,
		Status:               p.Status,
		Type:                 p.Type,
		Description:          p.Description,
		CreatedAt:            p.CreatedAt,
		UpdatedAt:            p.UpdatedAt,
	}
}

func toPromptRouterJSON(p *bedrockdriver.PromptRouter) promptRouterJSON {
	out := promptRouterJSON{
		PromptRouterARN:  p.ARN,
		PromptRouterName: p.Name,
		Models:           targetModelsOf(p.Models),
		Status:           p.Status,
		Type:             p.Type,
		Description:      p.Description,
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
	}
	if p.ResponseQualityDifference != nil {
		out.RoutingCriteria = &routingCriteriaJSON{ResponseQualityDifference: p.ResponseQualityDifference}
	}

	if p.FallbackModelARN != "" {
		out.FallbackModel = &promptRouterTargetModelJSON{ModelARN: p.FallbackModelARN}
	}

	return out
}

func toARPolicyJSON(p *bedrockdriver.AutomatedReasoningPolicy) arPolicyJSON {
	return arPolicyJSON{
		PolicyARN:      p.ARN,
		PolicyID:       p.ID,
		Name:           p.Name,
		Version:        p.Version,
		DefinitionHash: p.DefinitionHash,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
		Description:    p.Description,
		KMSKeyARN:      p.KMSKeyARN,
	}
}

func toARPolicySummaryJSON(p *bedrockdriver.AutomatedReasoningPolicy) arPolicySummaryJSON {
	return arPolicySummaryJSON{
		PolicyARN:   p.ARN,
		PolicyID:    p.ID,
		Name:        p.Name,
		Version:     p.Version,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
		Description: p.Description,
	}
}
