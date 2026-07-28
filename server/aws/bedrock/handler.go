// Package bedrock implements the AWS Bedrock restJson1 control-plane API and
// the bedrock-runtime InvokeModel / Converse data-plane API as a
// server.Handler. Point the real aws-sdk-go-v2/service/bedrock and
// .../bedrockruntime clients at a Server registered with this handler and the
// foundation-model catalog, custom-model customization lifecycle, and emulated
// inference all work end-to-end against an in-memory Bedrock driver.
//
// URL shapes follow what the SDKs emit:
//
//	GET    /foundation-models                          — ListFoundationModels
//	GET    /foundation-models/{modelId}                — GetFoundationModel
//	POST   /model-customization-jobs                   — CreateModelCustomizationJob
//	GET    /model-customization-jobs                   — ListModelCustomizationJobs
//	GET    /model-customization-jobs/{jobIdentifier}   — GetModelCustomizationJob
//	GET    /custom-models                              — ListCustomModels
//	GET    /custom-models/{modelIdentifier}            — GetCustomModel
//	DELETE /custom-models/{modelIdentifier}            — DeleteCustomModel
//	POST   /model/{modelId}/invoke                     — InvokeModel
//	POST   /model/{modelId}/converse                   — Converse
//	POST   /model/{modelId}/count-tokens               — CountTokens
//	POST   /guardrail/{id}/version/{version}/apply     — ApplyGuardrail
//	POST   /tagResource                                — TagResource
//	POST   /untagResource                              — UntagResource
//	POST   /listTagsForResource                        — ListTagsForResource
//
// The Matches predicate is rooted at these prefixes so it does not shadow the
// catch-all S3 handler that may be registered alongside.
package bedrock

import (
	"net/http"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	prefixFoundation = "/foundation-models"
	prefixJobs       = "/model-customization-jobs"
	prefixCustom     = "/custom-models"
	prefixRuntime    = "/model/"

	prefixGuardrails    = "/guardrails"
	prefixProvisioned   = "/provisioned-model-throughput"
	pathProvisionedList = "/provisioned-model-throughputs"
	pathLogging         = "/logging/modelinvocations"

	pathTagResource   = "/tagResource"
	pathUntagResource = "/untagResource"
	pathListTags      = "/listTagsForResource"

	// prefixApplyGuardrail is the singular /guardrail/ runtime prefix, distinct
	// from the plural /guardrails control-plane collection.
	prefixApplyGuardrail = "/guardrail/"

	prefixAsyncInvoke = "/async-invoke"
	prefixImportJobs  = "/model-import-jobs"
	prefixCopyJobs    = "/model-copy-jobs"
	prefixEvalJobs    = "/evaluation-jobs"
	// prefixEvalJobStop is the singular /evaluation-job/ prefix used only by
	// StopEvaluationJob (POST /evaluation-job/{id}/stop), distinct from the
	// plural /evaluation-jobs collection.
	prefixEvalJobStop = "/evaluation-job/"

	prefixInferenceProfiles = "/inference-profiles"
	prefixPromptRouters     = "/prompt-routers"
	prefixARPolicies        = "/automated-reasoning-policies"

	prefixMarketplaceEndpoints  = "/marketplace-model/endpoints"
	prefixListFMAgreementOffers = "/list-foundation-model-agreement-offers"
	prefixFMAvailability        = "/foundation-model-availability"
	pathCreateFMAgreement       = "/create-foundation-model-agreement"
	pathDeleteFMAgreement       = "/delete-foundation-model-agreement"

	suffixRegistration = "/registration"

	actionInvoke         = "invoke"
	actionConverse       = "converse"
	actionCountTokens    = "count-tokens"
	actionConverseStream = "converse-stream"
	actionInvokeStream   = "invoke-with-response-stream"
)

// Handler serves AWS Bedrock restJson1 requests against a Bedrock driver.
type Handler struct {
	bedrock bedrockdriver.Bedrock
}

// New returns a Bedrock handler backed by drv.
func New(drv bedrockdriver.Bedrock) *Handler {
	return &Handler{bedrock: drv}
}

// collectionPrefixes are the URL prefixes that own both a collection path and
// a "/{id}" resource path.
//
//nolint:gochecknoglobals // immutable routing table shared by Matches and ServeHTTP
var collectionPrefixes = []string{prefixFoundation, prefixJobs, prefixCustom, prefixGuardrails, prefixProvisioned}

// underPrefix reports whether p equals pre or is a child path of pre.
func underPrefix(p, pre string) bool {
	return p == pre || strings.HasPrefix(p, pre+"/")
}

// claims reports whether path p belongs to this handler.
func claims(p string) bool {
	for _, pre := range collectionPrefixes {
		if underPrefix(p, pre) {
			return true
		}
	}

	if strings.HasPrefix(p, prefixRuntime) || strings.HasPrefix(p, prefixApplyGuardrail) {
		return true
	}

	return claimsExtra(p)
}

// claimsExtra reports whether p belongs to a singleton or predicate-routed
// surface not covered by the collection prefixes. Split from claims to keep
// each function's cyclomatic complexity small.
func claimsExtra(p string) bool {
	return p == pathProvisionedList || p == pathLogging || isTagPath(p) ||
		isAsyncOrJobPath(p) || isRegistryPath(p) || isMarketplaceOrAgreementPath(p)
}

// marketplaceAgreementPrefixes are the collection/action prefixes for the
// marketplace-model-endpoint and foundation-model-agreement surfaces.
//
//nolint:gochecknoglobals // immutable routing table shared by claims and ServeHTTP
var marketplaceAgreementPrefixes = []string{prefixMarketplaceEndpoints, prefixListFMAgreementOffers, prefixFMAvailability}

// isMarketplaceOrAgreementPath reports whether p belongs to the
// marketplace-model-endpoint or foundation-model-agreement surface.
func isMarketplaceOrAgreementPath(p string) bool {
	if p == pathCreateFMAgreement || p == pathDeleteFMAgreement {
		return true
	}

	for _, pre := range marketplaceAgreementPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}

	return false
}

// registryPrefixes are the collection prefixes for the inference-profile,
// prompt-router, and automated-reasoning-policy control-plane surfaces.
//
//nolint:gochecknoglobals // immutable routing table shared by claims and ServeHTTP
var registryPrefixes = []string{prefixInferenceProfiles, prefixPromptRouters, prefixARPolicies}

// isRegistryPath reports whether p belongs to a control-plane registry surface.
func isRegistryPath(p string) bool {
	for _, pre := range registryPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}

	return false
}

// asyncJobPrefixes are the collection prefixes for the async-invoke and
// control-plane job surfaces.
//
//nolint:gochecknoglobals // immutable routing table shared by claims and ServeHTTP
var asyncJobPrefixes = []string{prefixAsyncInvoke, prefixImportJobs, prefixCopyJobs, prefixEvalJobs}

// isAsyncOrJobPath reports whether p belongs to the async-invoke or job surface.
func isAsyncOrJobPath(p string) bool {
	for _, pre := range asyncJobPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}

	return strings.HasPrefix(p, prefixEvalJobStop)
}

// Matches claims the Bedrock control-plane and runtime URL prefixes.
func (*Handler) Matches(r *http.Request) bool {
	return claims(r.URL.Path)
}

// ServeHTTP routes by URL prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	switch {
	case underPrefix(p, prefixFoundation):
		h.serveFoundation(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixFoundation), "/"))
	case underPrefix(p, prefixJobs):
		h.serveJobs(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixJobs), "/"))
	case underPrefix(p, prefixCustom):
		h.serveCustomModels(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixCustom), "/"))
	case strings.HasPrefix(p, prefixRuntime):
		h.serveRuntime(w, r, strings.TrimPrefix(p, prefixRuntime))
	case isAsyncOrJobPath(p):
		h.serveAsyncJobs(w, r, p)
	case isRegistryPath(p):
		h.serveRegistries(w, r, p)
	case isMarketplaceOrAgreementPath(p):
		h.serveMarketplaceAgreements(w, r, p)
	default:
		h.serveManagement(w, r, p)
	}
}

// isTagPath reports whether p is one of the resource-tagging endpoints.
func isTagPath(p string) bool {
	return p == pathTagResource || p == pathUntagResource || p == pathListTags
}

// serveTags routes the resource-tagging surface. Each path is POST-only.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, p string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch p {
	case pathTagResource:
		h.tagResource(w, r)
	case pathUntagResource:
		h.untagResource(w, r)
	case pathListTags:
		h.listTagsForResource(w, r)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+p)
	}
}

// serveApplyGuardrail handles POST
// /guardrail/{guardrailIdentifier}/version/{guardrailVersion}/apply. rest is the
// path with the /guardrail/ prefix already trimmed.
func (h *Handler) serveApplyGuardrail(w http.ResponseWriter, r *http.Request, rest string) {
	// Shape: {guardrailIdentifier}/version/{guardrailVersion}/apply. The
	// identifier may be an ARN containing slashes, so anchor on the fixed
	// "/version/" separator and the "/apply" suffix rather than a fixed split.
	body, ok := strings.CutSuffix(rest, "/apply")
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported guardrail path")

		return
	}

	idx := strings.LastIndex(body, "/version/")
	if idx < 0 {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported guardrail path")

		return
	}

	identifier, version := body[:idx], body[idx+len("/version/"):]

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	h.applyGuardrail(w, r, identifier, version)
}

// serveManagement routes the guardrail, provisioned-throughput, and invocation
// logging surfaces. Split out of ServeHTTP to keep each dispatcher small.
func (h *Handler) serveManagement(w http.ResponseWriter, r *http.Request, p string) {
	switch {
	case p == pathProvisionedList:
		h.serveProvisionedList(w, r)
	case p == prefixProvisioned || strings.HasPrefix(p, prefixProvisioned+"/"):
		h.serveProvisioned(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixProvisioned), "/"))
	case p == prefixGuardrails || strings.HasPrefix(p, prefixGuardrails+"/"):
		h.serveGuardrails(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixGuardrails), "/"))
	case p == pathLogging:
		h.serveLogging(w, r)
	case isTagPath(p):
		h.serveTags(w, r, p)
	case strings.HasPrefix(p, prefixApplyGuardrail):
		h.serveApplyGuardrail(w, r, strings.TrimPrefix(p, prefixApplyGuardrail))
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+p)
	}
}

// serveGuardrails handles /guardrails[/{id}].
func (h *Handler) serveGuardrails(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createGuardrail(w, r)
		case http.MethodGet:
			h.listGuardrails(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.createGuardrailVersion(w, r, id)
	case http.MethodGet:
		h.getGuardrail(w, r, id)
	case http.MethodPut:
		h.updateGuardrail(w, r, id)
	case http.MethodDelete:
		h.deleteGuardrail(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveProvisioned handles /provisioned-model-throughput[/{id}]. The bare path
// is create-only (POST); listing uses the plural path.
func (h *Handler) serveProvisioned(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		h.createProvisioned(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getProvisioned(w, r, id)
	case http.MethodDelete:
		h.deleteProvisioned(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveProvisionedList handles GET /provisioned-model-throughputs.
func (h *Handler) serveProvisionedList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.listProvisioned(w, r)
}

// serveLogging handles /logging/modelinvocations.
func (h *Handler) serveLogging(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		h.putLogging(w, r)
	case http.MethodGet:
		h.getLogging(w, r)
	case http.MethodDelete:
		h.deleteLogging(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveFoundation handles /foundation-models[/{id}]. id is "" for the list.
func (h *Handler) serveFoundation(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	if id == "" {
		h.listFoundationModels(w, r)

		return
	}

	h.getFoundationModel(w, r, id)
}

// serveJobs handles /model-customization-jobs[/{id}]. id is "" for the
// collection (POST create, GET list).
func (h *Handler) serveJobs(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createCustomizationJob(w, r)
		case http.MethodGet:
			h.listCustomizationJobs(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.getCustomizationJob(w, r, id)
}

// serveCustomModels handles /custom-models[/{id}].
func (h *Handler) serveCustomModels(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		h.listCustomModels(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCustomModel(w, r, id)
	case http.MethodDelete:
		h.deleteCustomModel(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveRuntime handles /model/{modelId}/{invoke|converse}. modelId may contain
// slashes (ARNs), so the action is split off the tail.
func (h *Handler) serveRuntime(w http.ResponseWriter, r *http.Request, rest string) {
	idx := strings.LastIndex(rest, "/")
	if idx < 0 {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported runtime path")

		return
	}

	modelID, action := rest[:idx], rest[idx+1:]

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch action {
	case actionInvoke:
		h.invokeModel(w, r, modelID)
	case actionConverse:
		h.converse(w, r, modelID)
	case actionConverseStream:
		h.converseStream(w, r, modelID)
	case actionInvokeStream:
		h.invokeModelStream(w, r, modelID)
	case actionCountTokens:
		h.countTokens(w, r, modelID)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unknown runtime action: "+action)
	}
}
