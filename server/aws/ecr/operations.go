package ecr

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request) {
	var req createRepositoryRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	repo, err := h.registry.CreateRepository(r.Context(), crdriver.RepositoryConfig{
		Name:               req.RepositoryName,
		Tags:               tagsToMap(req.Tags),
		ImageScanOnPush:    req.ImageScanningConfiguration.ScanOnPush,
		ImageTagMutability: req.ImageTagMutability,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := toRepositoryJSON(repo)
	out.ImageTagMutability = req.ImageTagMutability

	wire.WriteJSON(w, createRepositoryResponse{Repository: out})
}

func (h *Handler) describeRepositories(w http.ResponseWriter, r *http.Request) {
	var req describeRepositoriesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	repos, err := h.collectRepositories(r, req.RepositoryNames)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]repositoryJSON, 0, len(repos))
	for i := range repos {
		out = append(out, toRepositoryJSON(&repos[i]))
	}

	wire.WriteJSON(w, describeRepositoriesResponse{Repositories: out})
}

// collectRepositories returns the named repositories, or all of them when no
// names are given.
func (h *Handler) collectRepositories(r *http.Request, names []string) ([]crdriver.Repository, error) {
	if len(names) == 0 {
		return h.registry.ListRepositories(r.Context())
	}

	repos := make([]crdriver.Repository, 0, len(names))

	for _, name := range names {
		rp, err := h.registry.GetRepository(r.Context(), name)
		if err != nil {
			return nil, err
		}

		repos = append(repos, *rp)
	}

	return repos, nil
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request) {
	var req deleteRepositoryRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// ECR echoes the deleted repository, so capture it before removal.
	repo, err := h.registry.GetRepository(r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.registry.DeleteRepository(r.Context(), req.RepositoryName, req.Force); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, deleteRepositoryResponse{Repository: toRepositoryJSON(repo)})
}

func (h *Handler) putImage(w http.ResponseWriter, r *http.Request) {
	var req putImageRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	detail, err := h.registry.PutImage(r.Context(), &crdriver.ImageManifest{
		Repository: req.RepositoryName,
		Tag:        req.ImageTag,
		Digest:     req.ImageDigest,
		MediaType:  req.ImageManifestMediaType,
		SizeBytes:  int64(len(req.ImageManifest)),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, putImageResponse{Image: imageJSON{
		RegistryID:             detail.RegistryID,
		RepositoryName:         detail.Repository,
		ImageID:                imageIDJSON{ImageDigest: detail.Digest, ImageTag: req.ImageTag},
		ImageManifest:          req.ImageManifest,
		ImageManifestMediaType: req.ImageManifestMediaType,
	}})
}

func (h *Handler) listImages(w http.ResponseWriter, r *http.Request) {
	var req repositoryNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	images, err := h.registry.ListImages(r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	ids := make([]imageIDJSON, 0, len(images))
	for i := range images {
		ids = append(ids, imageIDsForDetail(&images[i])...)
	}

	wire.WriteJSON(w, listImagesResponse{ImageIDs: ids})
}

// imageIDsForDetail expands an image into one id per tag (real ECR lists each
// tag separately), or a digest-only id when untagged.
func imageIDsForDetail(d *crdriver.ImageDetail) []imageIDJSON {
	ids := make([]imageIDJSON, 0, len(d.Tags))

	for _, tag := range d.Tags {
		if tag == "" {
			continue
		}

		ids = append(ids, imageIDJSON{ImageDigest: d.Digest, ImageTag: tag})
	}

	if len(ids) == 0 {
		return []imageIDJSON{{ImageDigest: d.Digest}}
	}

	return ids
}

func (h *Handler) describeImages(w http.ResponseWriter, r *http.Request) {
	var req imageIDsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	images, err := h.registry.ListImages(r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	details := make([]imageDetailJSON, 0, len(images))

	for i := range images {
		if len(req.ImageIDs) > 0 && !matchesAnyID(&images[i], req.ImageIDs) {
			continue
		}

		details = append(details, toImageDetailJSON(&images[i]))
	}

	wire.WriteJSON(w, describeImagesResponse{ImageDetails: details})
}

func (h *Handler) batchDeleteImage(w http.ResponseWriter, r *http.Request) {
	var req imageIDsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// A missing repository is a thrown error; missing images become per-image
	// failures, matching real ECR.
	if _, err := h.registry.GetRepository(r.Context(), req.RepositoryName); err != nil {
		writeErr(w, err)
		return
	}

	deleted := make([]imageIDJSON, 0, len(req.ImageIDs))
	failures := make([]imageFailureJSON, 0)

	for _, id := range req.ImageIDs {
		if err := h.registry.DeleteImage(r.Context(), req.RepositoryName, imageReference(id)); err != nil {
			failures = append(failures, imageFailureJSON{
				ImageID:       id,
				FailureCode:   "ImageNotFound",
				FailureReason: "Requested image not found",
			})

			continue
		}

		deleted = append(deleted, id)
	}

	wire.WriteJSON(w, batchDeleteImageResponse{ImageIDs: deleted, Failures: failures})
}

func tagsToMap(tags []tagJSON) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func matchesAnyID(d *crdriver.ImageDetail, ids []imageIDJSON) bool {
	for _, id := range ids {
		if id.ImageDigest != "" && id.ImageDigest == d.Digest {
			return true
		}

		if id.ImageTag != "" && containsTag(d.Tags, id.ImageTag) {
			return true
		}
	}

	return false
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}

	return false
}
