package artifactregistry

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// serveIAM routes the repository IAM colon-verbs through the shared resource
// IAM store, so etags follow the real read-modify-write contract (the
// google_artifact_registry_repository_iam_* Terraform flow). CloudEmu does not
// enforce IAM.
func (h *Handler) serveIAM(w http.ResponseWriter, r *http.Request, rt *route) {
	if !gcpiam.IsVerb(rt.verb) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb "+rt.verb)
		return
	}

	// All verbs act on an existing repository (real AR never serves IAM for a
	// repository that does not exist).
	if _, err := h.registry.GetRepository(r.Context(), rt.repository); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcpiam.Serve(w, r, rt.verb, repositoryResourceName(rt.project, rt.location, rt.repository), h.iam)
}
