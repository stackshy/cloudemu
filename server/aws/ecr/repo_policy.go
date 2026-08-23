package ecr

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// repoPolicyManager is the AWS-specific ECR repository-policy surface, asserted
// against the provider (not part of the portable ContainerRegistry driver).
type repoPolicyManager interface {
	SetRepositoryPolicy(ctx context.Context, repository, policyText string) (string, error)
	GetRepositoryPolicy(ctx context.Context, repository string) (string, error)
	DeleteRepositoryPolicy(ctx context.Context, repository string) (string, error)
}

func (h *Handler) repoPolicyMgr() (repoPolicyManager, bool) {
	m, ok := h.registry.(repoPolicyManager)

	return m, ok
}

func (h *Handler) setRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.repoPolicyMgr()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "repository policies not supported"))
		return
	}

	var req struct {
		RepositoryName string `json:"repositoryName"`
		PolicyText     string `json:"policyText"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := mgr.SetRepositoryPolicy(r.Context(), req.RepositoryName, req.PolicyText)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"repositoryName": req.RepositoryName, "policyText": policy})
}

func (h *Handler) getRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.repoPolicyMgr()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "repository policies not supported"))
		return
	}

	var req struct {
		RepositoryName string `json:"repositoryName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := mgr.GetRepositoryPolicy(r.Context(), req.RepositoryName)
	if err != nil {
		// Distinguish a missing repository from a repository that simply has no
		// policy: real ECR returns RepositoryPolicyNotFoundException for the
		// latter, RepositoryNotFoundException for the former.
		if _, repoErr := h.registry.GetRepository(r.Context(), req.RepositoryName); repoErr != nil {
			writeErr(w, repoErr)
			return
		}

		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryPolicyNotFoundException", err.Error())

		return
	}

	wire.WriteJSON(w, map[string]any{"repositoryName": req.RepositoryName, "policyText": policy})
}

func (h *Handler) deleteRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.repoPolicyMgr()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "repository policies not supported"))
		return
	}

	var req struct {
		RepositoryName string `json:"repositoryName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policy, err := mgr.DeleteRepositoryPolicy(r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"repositoryName": req.RepositoryName, "policyText": policy})
}
