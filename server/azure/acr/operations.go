package acr

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request) {
	repos, err := h.registry.ListRepositories(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	names := make([]string, 0, len(repos))
	for i := range repos {
		names = append(names, shortID(repos[i].Name))
	}

	writeJSON(w, http.StatusOK, catalogResponse{Repositories: names})
}

func (h *Handler) getRepositoryProperties(w http.ResponseWriter, r *http.Request, repo string) {
	rp, err := h.registry.GetRepository(r.Context(), repo)
	if err != nil {
		writeCErr(w, err)
		return
	}

	images, err := h.registry.ListImages(r.Context(), repo)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, repositoryProperties{
		Registry:             registryLoginServer,
		ImageName:            shortID(rp.Name),
		CreatedTime:          rp.CreatedAt,
		LastUpdateTime:       rp.CreatedAt,
		ManifestCount:        len(images),
		TagCount:             countTags(images),
		ChangeableAttributes: allEnabled(),
	})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, repo string) {
	images, err := h.registry.ListImages(r.Context(), repo)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, tagListResponse{
		Registry:  registryLoginServer,
		ImageName: repo,
		Tags:      toTagAttributes(images),
	})
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request, repo string) {
	if err := h.registry.DeleteRepository(r.Context(), repo, true); err != nil {
		writeCErr(w, err)
		return
	}

	// ACR's delete is asynchronous; the SDK accepts 202 Accepted.
	writeJSON(w, http.StatusAccepted, deleteRepositoryResponse{
		ManifestsDeleted: []string{},
		TagsDeleted:      []string{},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
