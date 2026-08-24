package acr

import (
	"encoding/json"
	"net/http"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request) {
	repos, err := h.registry.ListRepositories(r.Context())
	if err != nil {
		writeCErr(w, err)
		return
	}

	names := make([]string, 0, len(repos))
	for i := range repos {
		names = append(names, repoName(repos[i].Name))
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
		ImageName:            repoName(rp.Name),
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

// findTag locates the image carrying tag in repo and returns it, or ok=false.
func findTag(images []crdriver.ImageDetail, tag string) (crdriver.ImageDetail, bool) {
	for i := range images {
		for _, t := range images[i].Tags {
			if t == tag {
				return images[i], true
			}
		}
	}

	return crdriver.ImageDetail{}, false
}

func (h *Handler) getTagProperties(w http.ResponseWriter, r *http.Request, repo, tag string) {
	images, err := h.registry.ListImages(r.Context(), repo)
	if err != nil {
		writeCErr(w, err)
		return
	}

	img, ok := findTag(images, tag)
	if !ok {
		writeErr(w, http.StatusNotFound, "TAG_UNKNOWN", "tag "+tag+" not found in repository "+repo)
		return
	}

	writeJSON(w, http.StatusOK, tagProperties{
		Registry:  registryLoginServer,
		ImageName: repo,
		Tag: tagAttributes{
			Name:                 tag,
			Digest:               img.Digest,
			CreatedTime:          img.PushedAt,
			LastUpdateTime:       img.PushedAt,
			ChangeableAttributes: allEnabled(),
		},
	})
}

func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request, repo, tag string) {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "UNSUPPORTED", "tag deletion is not supported by this registry driver")
		return
	}

	if err := writer.DeleteTag(r.Context(), repo, tag); err != nil {
		writeCErr(w, err)
		return
	}

	// ACR answers DELETE _tags/{tag} with 202 Accepted and an empty body.
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) listManifests(w http.ResponseWriter, r *http.Request, repo string) {
	images, err := h.registry.ListImages(r.Context(), repo)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, manifestList{
		Registry:  registryLoginServer,
		ImageName: repo,
		Manifests: toManifestAttributes(images),
	})
}

func (h *Handler) getManifestProperties(w http.ResponseWriter, r *http.Request, repo, digest string) {
	img, err := h.registry.GetImage(r.Context(), repo, digest)
	if err != nil {
		writeCErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, manifestProperties{
		Registry:  registryLoginServer,
		ImageName: repo,
		Manifest:  toManifestAttribute(img),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
