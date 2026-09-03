package ecr

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// lifecyclePreviewStatus is the only status the emulator ever reports: real
// ECR evaluates a preview asynchronously (IN_PROGRESS -> COMPLETE/FAILED),
// but the emulator computes it synchronously inside StartLifecyclePolicyPreview,
// so it is always immediately COMPLETE.
const lifecyclePreviewStatus = "COMPLETE"

// lifecyclePreviewer is the AWS-specific StartLifecyclePolicyPreview/
// GetLifecyclePolicyPreview surface. Azure ACR and GCP Artifact Registry have
// no lifecycle-preview API, so this is not part of the portable
// ContainerRegistry driver; the handler reaches it via type assertion, the
// same pattern used for PutImageTagMutability and PutImageScanningConfiguration.
type lifecyclePreviewer interface {
	PreviewLifecyclePolicy(
		ctx context.Context, repository string, override *crdriver.LifecyclePolicy,
	) ([]crdriver.LifecyclePreviewResult, string, error)
}

// lifecyclePreviewCache holds the synchronously computed result of the most
// recent StartLifecyclePolicyPreview call for one repository, replayed by
// GetLifecyclePolicyPreview.
type lifecyclePreviewCache struct {
	text    string
	results []crdriver.LifecyclePreviewResult
}

func (h *Handler) startLifecyclePolicyPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName      string `json:"repositoryName"`
		LifecyclePolicyText string `json:"lifecyclePolicyText"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	previewer, ok := h.registry.(lifecyclePreviewer)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "ServerException", "lifecycle policy preview not supported")
		return
	}

	var override *crdriver.LifecyclePolicy

	if req.LifecyclePolicyText != "" {
		parsed, err := parseLifecyclePolicyText(req.LifecyclePolicyText)
		if err != nil {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
			return
		}

		override = &parsed
	}

	results, text, err := previewer.PreviewLifecyclePolicy(r.Context(), req.RepositoryName, override)
	if err != nil {
		writeErr(w, err)
		return
	}

	h.previewMu.Lock()
	h.previews[req.RepositoryName] = lifecyclePreviewCache{text: text, results: results}
	h.previewMu.Unlock()

	resp := map[string]any{
		"repositoryName":      req.RepositoryName,
		"lifecyclePolicyText": text,
		"status":              lifecyclePreviewStatus,
	}

	if repo, err := h.registry.GetRepository(r.Context(), req.RepositoryName); err == nil {
		resp["registryId"] = repo.RegistryID
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) getLifecyclePolicyPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// A missing repository always takes precedence over a missing/stale cache
	// entry, so a repository deleted (and not re-previewed) since Start reports
	// RepositoryNotFoundException rather than replaying a stale preview.
	repo, err := h.registry.GetRepository(r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	h.previewMu.Lock()
	cache, ok := h.previews[req.RepositoryName]
	h.previewMu.Unlock()

	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "LifecyclePolicyPreviewNotFoundException",
			"no lifecycle policy preview found for repository "+req.RepositoryName+
				"; call StartLifecyclePolicyPreview first")

		return
	}

	wire.WriteJSON(w, map[string]any{
		"repositoryName":      req.RepositoryName,
		"registryId":          repo.RegistryID,
		"lifecyclePolicyText": cache.text,
		"status":              lifecyclePreviewStatus,
		"previewResults":      toLifecyclePreviewResultsJSON(cache.results),
		"summary":             map[string]any{"expiringImageTotalCount": len(cache.results)},
	})
}

func toLifecyclePreviewResultsJSON(results []crdriver.LifecyclePreviewResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))

	for _, res := range results {
		out = append(out, map[string]any{
			"imageDigest":         res.Digest,
			"imageTags":           res.Tags,
			"imagePushedAt":       epochSeconds(res.PushedAt),
			"appliedRulePriority": res.AppliedRulePriority,
			"action":              map[string]any{"type": "EXPIRE"},
		})
	}

	return out
}
