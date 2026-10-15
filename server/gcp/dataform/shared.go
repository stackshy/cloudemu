package dataform

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// Artifact Registry (and Secure Source Manager) serve the same
// /v1/.../locations/{l}/repositories paths. When they are mounted, Dataform
// claims a v1 request only when it is hinted, addresses a repository Dataform
// owns, creates one with a Dataform body, or lists a location where Dataform
// owns a repository. /v1beta1/ is Dataform's alone.

const maxProbeBytes = 1 << 20

// SetSharedPath turns on the shared v1 repositories rules.
func (h *Handler) SetSharedPath() { h.shared = true }

// matchesV1 decides a /v1/ repositories request.
func (h *Handler) matchesV1(r *http.Request, rt *route) bool {
	if sharedpath.Yield(r, sharedpath.Dataform, sharedpath.ArtifactRegistry, sharedpath.SecureSourceManager) {
		return false
	}

	if !h.shared || sharedpath.Is(r, sharedpath.Dataform) {
		return true
	}

	ctx := r.Context()

	if rt.repo != "" {
		_, err := h.db.GetRepository(ctx, rt.project, rt.location, rt.repo)

		return err == nil
	}

	switch r.Method {
	case http.MethodPost:
		return bodyLooksLikeDataform(r)
	case http.MethodGet:
		all, err := h.db.ListRepositories(ctx, rt.project, rt.location)

		return err == nil && len(all) > 0
	default:
		return false
	}
}

// bodyLooksLikeDataform reports a Repository body with no Artifact Registry
// format and at least one Dataform field. The body is restored.
func bodyLooksLikeDataform(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := wire.PeekBody(r, maxProbeBytes)

	if err != nil {
		return false
	}

	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}

	if _, has := probe["format"]; has {
		return false
	}

	for _, k := range []string{
		"gitRemoteSettings", "workspaceCompilationOverrides", "npmrcEnvironmentVariablesSecretVersion",
		"serviceAccount", "setAuthenticatedUserAdmin",
	} {
		if _, has := probe[k]; has {
			return true
		}
	}

	return false
}
