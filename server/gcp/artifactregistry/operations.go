package artifactregistry

import (
	"net/http"

	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	repoID := r.URL.Query().Get("repositoryId")

	var body repositoryJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	repo, err := h.registry.CreateRepository(r.Context(), crdriver.RepositoryConfig{
		Name: repoID,
		Tags: body.Labels,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, repoID, toRepositoryJSON(rt.project, rt.location, repo)))
}

func (h *Handler) getRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	repo, err := h.registry.GetRepository(r.Context(), rt.repository)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toRepositoryJSON(rt.project, rt.location, repo))
}

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request, rt *route) {
	repos, err := h.registry.ListRepositories(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]repositoryJSON, 0, len(repos))
	for i := range repos {
		out = append(out, toRepositoryJSON(rt.project, rt.location, &repos[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listRepositoriesResponse{Repositories: out})
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	// Artifact Registry's delete has no force flag; it removes the repository
	// and its contents.
	if err := h.registry.DeleteRepository(r.Context(), rt.repository, true); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, rt.repository, nil))
}

func (h *Handler) listDockerImages(w http.ResponseWriter, r *http.Request, rt *route) {
	images, err := h.registry.ListImages(r.Context(), rt.repository)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]dockerImageJSON, 0, len(images))
	for i := range images {
		out = append(out, toDockerImageJSON(rt.project, rt.location, rt.repository, &images[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listDockerImagesResponse{DockerImages: out})
}

// doneOperation builds a completed long-running operation envelope.
func doneOperation(rt *route, id string, response any) operationJSON {
	return operationJSON{
		Name:     "projects/" + rt.project + "/locations/" + rt.location + "/operations/op-" + id,
		Done:     true,
		Response: response,
	}
}
