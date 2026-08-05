package artifactregistry

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	repoID := r.URL.Query().Get("repositoryId")

	var body repositoryJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	// The driver models a Docker registry and has no format/description field,
	// so preserve the request's values in reserved tags and echo them on read.
	tags := make(map[string]string, len(body.Labels))
	for k, v := range body.Labels {
		tags[k] = v
	}

	if body.Format != "" {
		tags[formatTag] = body.Format
	}

	if body.Description != "" {
		tags[descriptionTag] = body.Description
	}

	repo, err := h.registry.CreateRepository(r.Context(), crdriver.RepositoryConfig{
		Name: repoID,
		Tags: tags,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, repoID,
		typedResponse(repositoryTypeURL, toRepositoryJSON(rt.project, rt.location, repo))))
}

// repositoryTypeURL is the protobuf Any type URL a GAPIC client expects in a
// done LRO's response so CreateRepositoryOperation.Wait() can decode it.
const repositoryTypeURL = "type.googleapis.com/google.devtools.artifactregistry.v1.Repository"

// typedResponse renders v as the JSON object a google.protobuf.Any expects: the
// resource's fields plus an "@type" URL. Without @type a GAPIC .Wait() cannot
// unmarshal the operation response.
func typedResponse(typeURL string, v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}

	m["@type"] = typeURL

	return m
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
