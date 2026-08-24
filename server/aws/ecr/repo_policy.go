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
	// The provider tags a missing repository (RepositoryNotFoundException) and a
	// repository with no policy (RepositoryPolicyNotFoundException) with the
	// precise exception name, which writeErr surfaces.
	h.runRepoPolicyOp(w, r, repoPolicyManager.GetRepositoryPolicy)
}

func (h *Handler) deleteRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	// A repository with no policy is RepositoryPolicyNotFoundException, a missing
	// repository is RepositoryNotFoundException — both carried by the provider's
	// tagged error and surfaced by writeErr.
	h.runRepoPolicyOp(w, r, repoPolicyManager.DeleteRepositoryPolicy)
}

// runRepoPolicyOp runs a repository-name-only policy operation (get or delete)
// and writes the resulting policyText, sharing the manager guard, request
// decode, and error mapping between the two handlers.
func (h *Handler) runRepoPolicyOp(
	w http.ResponseWriter, r *http.Request, op func(repoPolicyManager, context.Context, string) (string, error),
) {
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

	policy, err := op(mgr, r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"repositoryName": req.RepositoryName, "policyText": policy})
}
