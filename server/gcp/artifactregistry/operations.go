package artifactregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func (h *Handler) createRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	repoID := r.URL.Query().Get("repositoryId")

	var body repositoryJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	repo, err := h.registry.CreateRepository(r.Context(), crdriver.RepositoryConfig{
		Name: repoID,
		Tags: reservedTagsFrom(&body),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, repoID,
		typedResponse(repositoryTypeURL, toRepositoryJSON(rt.project, rt.location, repo, 0))))
}

// reservedTagsFrom folds the GCP-only Repository fields (format, description,
// mode, kmsKeyName) the driver does not model into reserved tags alongside the
// user labels, so they round-trip on read.
// reservedFieldCount is the number of GCP-only Repository fields folded into
// reserved tags (format, description, mode, kmsKeyName).
const reservedFieldCount = 4

func reservedTagsFrom(body *repositoryJSON) map[string]string {
	tags := make(map[string]string, len(body.Labels)+reservedFieldCount)
	for k, v := range body.Labels {
		tags[k] = v
	}

	if body.Format != "" {
		tags[formatTag] = string(body.Format)
	}

	if body.Description != "" {
		tags[descriptionTag] = body.Description
	}

	if body.Mode != "" {
		tags[modeTag] = body.Mode
	}

	if body.KmsKeyName != "" {
		tags[kmsKeyTag] = body.KmsKeyName
	}

	return tags
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

	gcprest.WriteJSON(w, http.StatusOK,
		toRepositoryJSON(rt.project, rt.location, repo, h.repoSizeBytes(r.Context(), rt.repository)))
}

func (h *Handler) listRepositories(w http.ResponseWriter, r *http.Request, rt *route) {
	repos, err := h.registry.ListRepositories(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(repos,
		func(a, b crdriver.Repository) bool { return repoName(a.Name) < repoName(b.Name) },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid page token")
		return
	}

	out := make([]repositoryJSON, 0, len(page.Items))

	for i := range page.Items {
		name := repoName(page.Items[i].Name)
		out = append(out, toRepositoryJSON(rt.project, rt.location, &page.Items[i], h.repoSizeBytes(r.Context(), name)))
	}

	gcprest.WriteJSON(w, http.StatusOK,
		listRepositoriesResponse{Repositories: out, NextPageToken: page.NextPageToken})
}

func (h *Handler) patchRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	updater, ok := h.registry.(repositoryUpdater)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "repository patch unsupported")
		return
	}

	current, err := h.registry.GetRepository(r.Context(), rt.repository)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var body repositoryJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	tags := applyPatch(current.Tags, &body, updateMaskFields(r))

	updated, err := updater.UpdateRepository(r.Context(), rt.repository, tags)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK,
		toRepositoryJSON(rt.project, rt.location, updated, h.repoSizeBytes(r.Context(), rt.repository)))
}

// repositoryUpdater is the optional driver extension the GCP mock implements to
// persist a repository patch. Providers without it get a 404 on PATCH.
type repositoryUpdater interface {
	UpdateRepository(ctx context.Context, name string, tags map[string]string) (*crdriver.Repository, error)
}

// applyPatch merges the patch body into the current reserved+label tag set,
// honoring the update mask. An empty mask (or "*") updates every settable field.
func applyPatch(current map[string]string, body *repositoryJSON, mask map[string]bool) map[string]string {
	tags := make(map[string]string, len(current))
	for k, v := range current {
		tags[k] = v
	}

	all := len(mask) == 0 || mask["*"]

	if all || mask["labels"] {
		replaceLabels(tags, body.Labels)
	}

	if all || mask["description"] {
		setOrDelete(tags, descriptionTag, body.Description)
	}

	if all || mask["kmsKeyName"] {
		setOrDelete(tags, kmsKeyTag, body.KmsKeyName)
	}

	return tags
}

// replaceLabels swaps every non-reserved (user label) key in tags for the ones
// in labels, leaving reserved cloudemu: keys intact.
func replaceLabels(tags, labels map[string]string) {
	for k := range tags {
		if !strings.HasPrefix(k, reservedTagPrefix) {
			delete(tags, k)
		}
	}

	for k, v := range labels {
		tags[k] = v
	}
}

func setOrDelete(tags map[string]string, key, val string) {
	if val == "" {
		delete(tags, key)
		return
	}

	tags[key] = val
}

// updateMaskFields parses the ?updateMask=labels,description query into a set.
func updateMaskFields(r *http.Request) map[string]bool {
	raw := r.URL.Query().Get("updateMask")
	if raw == "" {
		return nil
	}

	out := make(map[string]bool)

	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out[f] = true
		}
	}

	return out
}

func (h *Handler) deleteRepository(w http.ResponseWriter, r *http.Request, rt *route) {
	// Artifact Registry's delete has no force flag; it removes the repository
	// and its contents.
	if err := h.registry.DeleteRepository(r.Context(), rt.repository, true); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.mu.Lock()
	delete(h.policies, repositoryResourceName(rt.project, rt.location, rt.repository))
	h.mu.Unlock()

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, rt.repository, nil))
}

func (h *Handler) listDockerImages(w http.ResponseWriter, r *http.Request, rt *route) {
	images, err := h.registry.ListImages(r.Context(), rt.repository)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, ok := paginateImages(w, r, images)
	if !ok {
		return
	}

	out := make([]dockerImageJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toDockerImageJSON(rt.project, rt.location, rt.repository, &page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK,
		listDockerImagesResponse{DockerImages: out, NextPageToken: page.NextPageToken})
}

// paginateImages sorts images by digest and slices one page, writing a 400 and
// returning ok=false on a bad page token. Shared by every image-backed
// sub-collection (docker images, packages, files).
func paginateImages(
	w http.ResponseWriter, r *http.Request, images []crdriver.ImageDetail,
) (pagination.Page[crdriver.ImageDetail], bool) {
	page, err := pagination.PaginateSorted(images,
		func(a, b crdriver.ImageDetail) bool { return a.Digest < b.Digest },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid page token")
		return page, false
	}

	return page, true
}

// repoSizeBytes sums the image sizes in a repository. Best-effort: a missing
// repository (concurrent delete) contributes zero.
func (h *Handler) repoSizeBytes(ctx context.Context, repository string) int64 {
	images, err := h.registry.ListImages(ctx, repository)
	if err != nil {
		return 0
	}

	var total int64
	for i := range images {
		total += images[i].SizeBytes
	}

	return total
}

// pageSize reads the ?pageSize query parameter (0 when absent/invalid, which the
// pagination helper treats as its default).
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// doneOperation builds a completed long-running operation envelope.
func doneOperation(rt *route, id string, response any) operationJSON {
	return operationJSON{
		Name:     "projects/" + rt.project + "/locations/" + rt.location + "/operations/op-" + id,
		Done:     true,
		Response: response,
	}
}
