package acr

import (
	"context"
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
		ChangeableAttributes: h.repositoryAttrs(r.Context(), repo),
	})
}

// updateRepositoryProperties serves PATCH /acr/v1/{name} (changeableAttributes
// only — repository metadata such as name/createdTime is immutable).
func (h *Handler) updateRepositoryProperties(w http.ResponseWriter, r *http.Request, repo string) {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "UNSUPPORTED", "attribute updates are not supported by this registry driver")
		return
	}

	patch, ok := decodeAttributesPatch(w, r)
	if !ok {
		return
	}

	if _, err := writer.UpdateRepositoryAttributes(r.Context(), repo, patch.toDriver()); err != nil {
		writeCErr(w, err)
		return
	}

	h.getRepositoryProperties(w, r, repo)
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, repo string) {
	images, err := h.registry.ListImages(r.Context(), repo)
	if err != nil {
		writeCErr(w, err)
		return
	}

	ctx := r.Context()

	writeJSON(w, http.StatusOK, tagListResponse{
		Registry:  registryLoginServer,
		ImageName: repo,
		Tags: toTagAttributes(images, func(tag string) changeableAttributes {
			return h.tagAttrs(ctx, repo, tag)
		}),
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

func (h *Handler) getTagProperties(w http.ResponseWriter, r *http.Request, repo, tag string) {
	if _, err := h.registry.GetRepository(r.Context(), repo); err != nil {
		writeCErr(w, err)
		return
	}

	img, err := h.registry.GetImage(r.Context(), repo, tag)
	if err != nil {
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
			ChangeableAttributes: h.tagAttrs(r.Context(), repo, tag),
		},
	})
}

// updateTagProperties serves PATCH /acr/v1/{name}/_tags/{tag}.
func (h *Handler) updateTagProperties(w http.ResponseWriter, r *http.Request, repo, tag string) {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "UNSUPPORTED", "attribute updates are not supported by this registry driver")
		return
	}

	patch, ok := decodeAttributesPatch(w, r)
	if !ok {
		return
	}

	if _, err := writer.UpdateTagAttributes(r.Context(), repo, tag, patch.toDriver()); err != nil {
		writeCErr(w, err)
		return
	}

	h.getTagProperties(w, r, repo, tag)
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

	ctx := r.Context()

	writeJSON(w, http.StatusOK, manifestList{
		Registry:  registryLoginServer,
		ImageName: repo,
		Manifests: toManifestAttributes(images, func(digest string) changeableAttributes {
			return h.manifestAttrs(ctx, repo, digest)
		}),
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
		Manifest:  toManifestAttribute(img, h.manifestAttrs(r.Context(), repo, digest)),
	})
}

// updateManifestProperties serves PATCH /acr/v1/{name}/_manifests/{digest}.
func (h *Handler) updateManifestProperties(w http.ResponseWriter, r *http.Request, repo, digest string) {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "UNSUPPORTED", "attribute updates are not supported by this registry driver")
		return
	}

	patch, ok := decodeAttributesPatch(w, r)
	if !ok {
		return
	}

	if _, err := writer.UpdateManifestAttributes(r.Context(), repo, digest, patch.toDriver()); err != nil {
		writeCErr(w, err)
		return
	}

	h.getManifestProperties(w, r, repo, digest)
}

// decodeAttributesPatch decodes a changeableAttributes PATCH body, writing a
// 400 response and returning ok=false on malformed JSON.
func decodeAttributesPatch(w http.ResponseWriter, r *http.Request) (changeableAttributesPatch, bool) {
	var patch changeableAttributesPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed changeableAttributes body")
		return changeableAttributesPatch{}, false
	}

	return patch, true
}

// repositoryAttrs resolves repo's changeableAttributes, defaulting to fully
// enabled when the backing driver does not implement attribute storage.
func (h *Handler) repositoryAttrs(ctx context.Context, repo string) changeableAttributes {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		return allEnabled()
	}

	attrs, err := writer.GetRepositoryAttributes(ctx, repo)
	if err != nil {
		return allEnabled()
	}

	return toChangeableAttributes(attrs)
}

// tagAttrs resolves tag's changeableAttributes, defaulting to fully enabled
// when the backing driver does not implement attribute storage.
func (h *Handler) tagAttrs(ctx context.Context, repo, tag string) changeableAttributes {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		return allEnabled()
	}

	attrs, err := writer.GetTagAttributes(ctx, repo, tag)
	if err != nil {
		return allEnabled()
	}

	return toChangeableAttributes(attrs)
}

// manifestAttrs resolves digest's changeableAttributes, defaulting to fully
// enabled when the backing driver does not implement attribute storage.
func (h *Handler) manifestAttrs(ctx context.Context, repo, digest string) changeableAttributes {
	writer, ok := h.registry.(crdriver.AzureRepositoryWriter)
	if !ok {
		return allEnabled()
	}

	attrs, err := writer.GetManifestAttributes(ctx, repo, digest)
	if err != nil {
		return allEnabled()
	}

	return toChangeableAttributes(attrs)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
