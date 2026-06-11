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
//
// The Matches predicate is rooted at these prefixes so it does not shadow the
// catch-all S3 handler that may be registered alongside.
package bedrock

import (
	"net/http"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/bedrock/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	prefixFoundation = "/foundation-models"
	prefixJobs       = "/model-customization-jobs"
	prefixCustom     = "/custom-models"
	prefixRuntime    = "/model/"

	actionInvoke   = "invoke"
	actionConverse = "converse"
)

// Handler serves AWS Bedrock restJson1 requests against a Bedrock driver.
type Handler struct {
	bedrock bedrockdriver.Bedrock
}

// New returns a Bedrock handler backed by drv.
func New(drv bedrockdriver.Bedrock) *Handler {
	return &Handler{bedrock: drv}
}

// Matches claims the Bedrock control-plane and runtime URL prefixes.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return p == prefixFoundation || strings.HasPrefix(p, prefixFoundation+"/") ||
		p == prefixJobs || strings.HasPrefix(p, prefixJobs+"/") ||
		p == prefixCustom || strings.HasPrefix(p, prefixCustom+"/") ||
		strings.HasPrefix(p, prefixRuntime)
}

// ServeHTTP routes by URL prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	switch {
	case p == prefixFoundation || strings.HasPrefix(p, prefixFoundation+"/"):
		h.serveFoundation(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixFoundation), "/"))
	case p == prefixJobs || strings.HasPrefix(p, prefixJobs+"/"):
		h.serveJobs(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixJobs), "/"))
	case p == prefixCustom || strings.HasPrefix(p, prefixCustom+"/"):
		h.serveCustomModels(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixCustom), "/"))
	case strings.HasPrefix(p, prefixRuntime):
		h.serveRuntime(w, r, strings.TrimPrefix(p, prefixRuntime))
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+p)
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
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unknown runtime action: "+action)
	}
}
