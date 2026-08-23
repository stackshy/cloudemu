package ecr

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
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

	wire.WriteJSON(w, createRepositoryResponse{Repository: toRepositoryJSON(repo)})
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

	page, err := pagination.PaginateSorted(repos,
		func(a, b crdriver.Repository) bool { return a.Name < b.Name },
		req.NextToken, req.MaxResults)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
		return
	}

	out := make([]repositoryJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toRepositoryJSON(&page.Items[i]))
	}

	wire.WriteJSON(w, describeRepositoriesResponse{Repositories: out, NextToken: page.NextPageToken})
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
		Manifest:   req.ImageManifest,
	})
	if err != nil {
		writePutImageErr(w, err)
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

	page, err := pagination.PaginateSorted(ids, lessImageID, req.NextToken, req.MaxResults)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
		return
	}

	wire.WriteJSON(w, listImagesResponse{ImageIDs: page.Items, NextToken: page.NextPageToken})
}

// lessImageID orders image ids deterministically (digest, then tag) so that
// offset-based pagination tokens stay stable across calls.
func lessImageID(a, b imageIDJSON) bool {
	if a.ImageDigest != b.ImageDigest {
		return a.ImageDigest < b.ImageDigest
	}

	return a.ImageTag < b.ImageTag
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

	// Real ECR throws ImageNotFoundException when a requested imageId does not
	// resolve to any image in the repository.
	if missing := firstUnmatchedID(images, req.ImageIDs); missing != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "ImageNotFoundException",
			"The image with imageId "+imageReference(*missing)+" does not exist within the repository")
		return
	}

	details := make([]imageDetailJSON, 0, len(images))

	for i := range images {
		if len(req.ImageIDs) > 0 && !matchesAnyID(&images[i], req.ImageIDs) {
			continue
		}

		details = append(details, toImageDetailJSON(&images[i]))
	}

	page, err := pagination.PaginateSorted(details,
		func(a, b imageDetailJSON) bool { return a.ImageDigest < b.ImageDigest },
		req.NextToken, req.MaxResults)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
		return
	}

	wire.WriteJSON(w, describeImagesResponse{ImageDetails: page.Items, NextToken: page.NextPageToken})
}

// firstUnmatchedID returns the first requested image id that matches no image,
// or nil when every requested id resolves (or none were requested).
func firstUnmatchedID(images []crdriver.ImageDetail, ids []imageIDJSON) *imageIDJSON {
	for i := range ids {
		matched := false

		for j := range images {
			if matchesAnyID(&images[j], []imageIDJSON{ids[i]}) {
				matched = true
				break
			}
		}

		if !matched {
			return &ids[i]
		}
	}

	return nil
}

func (h *Handler) batchGetImage(w http.ResponseWriter, r *http.Request) {
	var req imageIDsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	images, err := h.registry.ListImages(r.Context(), req.RepositoryName)
	if err != nil {
		writeErr(w, err)
		return
	}

	found := make([]imageJSON, 0, len(req.ImageIDs))
	failures := make([]imageFailureJSON, 0)

	for _, id := range req.ImageIDs {
		detail := findImageDetail(images, id)
		if detail == nil {
			failures = append(failures, imageFailureJSON{
				ImageID:       id,
				FailureCode:   "ImageNotFound",
				FailureReason: "Requested image not found",
			})

			continue
		}

		found = append(found, imageJSON{
			RegistryID:             detail.RegistryID,
			RepositoryName:         detail.Repository,
			ImageID:                imageIDJSON{ImageDigest: detail.Digest, ImageTag: returnedTag(detail, id)},
			ImageManifest:          detail.Manifest,
			ImageManifestMediaType: detail.MediaType,
		})
	}

	wire.WriteJSON(w, batchGetImageResponse{Images: found, Failures: failures})
}

// findImageDetail resolves a requested image id (digest or tag) to a stored
// image detail, or nil when none matches.
func findImageDetail(images []crdriver.ImageDetail, id imageIDJSON) *crdriver.ImageDetail {
	for i := range images {
		if id.ImageDigest != "" && id.ImageDigest == images[i].Digest {
			return &images[i]
		}

		if id.ImageTag != "" && containsTag(images[i].Tags, id.ImageTag) {
			return &images[i]
		}
	}

	return nil
}

// returnedTag picks the tag to echo for a resolved image: the requested tag if
// one was given, otherwise the image's first tag.
func returnedTag(detail *crdriver.ImageDetail, id imageIDJSON) string {
	if id.ImageTag != "" {
		return id.ImageTag
	}

	if len(detail.Tags) > 0 {
		return detail.Tags[0]
	}

	return ""
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
