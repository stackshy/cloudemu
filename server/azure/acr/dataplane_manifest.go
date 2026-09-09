package acr

import (
	"net/http"
	"strings"
)

// v2Prefix and v2ManifestsMarker locate the OCI-distribution manifest path
// azcontainerregistry's Client.GetManifest / DeleteManifest / UploadManifest
// actually issue requests against: "/v2/{name}/manifests/{reference}". This
// is a *different* URL family from the "/acr/v1/{name}/_manifests/{digest}"
// changeableAttributes path served by operations.go — real ACR exposes both:
// /acr/v1 for catalog metadata (list/get/patch), /v2 for the manifest content
// itself (get/delete/push), per the Docker Registry HTTP API V2 the ACR data
// plane implements alongside its own /acr/v1 extension.
const (
	v2Prefix         = "/v2/"
	v2ManifestMarker = "manifests"

	// defaultManifestMediaType is used when an image was pushed without a
	// media type, mirroring the provider's own fallback.
	defaultManifestMediaType = "application/vnd.docker.distribution.manifest.v2+json"
)

// parseV2ManifestPath splits a /v2/{name}/manifests/{reference} path into the
// repository name and reference (tag or digest). The repository name may be
// hierarchical (e.g. "team/app"), so the split hinges on the "/manifests/"
// marker segment rather than the first slash, mirroring parseACRPath.
func parseV2ManifestPath(path string) (repo, reference string, ok bool) {
	if !strings.HasPrefix(path, v2Prefix) {
		return "", "", false
	}

	tail := strings.TrimPrefix(path, v2Prefix)

	idx := strings.Index(tail, "/"+v2ManifestMarker+"/")
	if idx < 0 {
		return "", "", false
	}

	repo = tail[:idx]
	reference = tail[idx+len(v2ManifestMarker)+2:]

	if repo == "" || reference == "" {
		return "", "", false
	}

	return repo, reference, true
}

// serveV2Manifest routes GET (fetch manifest content) and DELETE (delete
// manifest) against the shared ContainerRegistry driver's already-modeled
// GetImage/DeleteImage. PUT (push) is not modeled — cloudemu has no real OCI
// blob storage — and answers a clean 405 instead of silently falling through
// to an unrelated handler.
func (h *Handler) serveV2Manifest(w http.ResponseWriter, r *http.Request, repo, reference string) {
	switch r.Method {
	case http.MethodGet:
		h.getManifestContent(w, r, repo, reference)
	case http.MethodDelete:
		h.deleteManifestContent(w, r, repo, reference)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported ACR operation")
	}
}

// getManifestContent serves GET /v2/{name}/manifests/{reference}, returning
// the raw manifest document azcontainerregistry's GetManifest expects in the
// response body with a Docker-Content-Digest header.
func (h *Handler) getManifestContent(w http.ResponseWriter, r *http.Request, repo, reference string) {
	if _, err := h.registry.GetRepository(r.Context(), repo); err != nil {
		writeErr(w, http.StatusNotFound, "NAME_UNKNOWN", "repository "+repo+" not found")
		return
	}

	img, err := h.registry.GetImage(r.Context(), repo, reference)
	if err != nil {
		writeErr(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest "+reference+" not found in repository "+repo)
		return
	}

	body := img.Manifest
	if body == "" {
		body = "{}"
	}

	mediaType := img.MediaType
	if mediaType == "" {
		mediaType = defaultManifestMediaType
	}

	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Docker-Content-Digest", img.Digest)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body)) //nolint:gosec // G705: manifest JSON/OCI document echoed with its own Content-Type, not an HTML sink.
}

// deleteManifestContent serves DELETE /v2/{name}/manifests/{reference}. Real
// ACR (and azcontainerregistry's DeleteManifest, which treats both 202 and
// 404 as success) answers 202 Accepted; a missing repository or manifest is
// reported with the OCI-distribution NAME_UNKNOWN/MANIFEST_UNKNOWN error
// codes rather than a generic 404.
func (h *Handler) deleteManifestContent(w http.ResponseWriter, r *http.Request, repo, reference string) {
	if _, err := h.registry.GetRepository(r.Context(), repo); err != nil {
		writeErr(w, http.StatusNotFound, "NAME_UNKNOWN", "repository "+repo+" not found")
		return
	}

	if err := h.registry.DeleteImage(r.Context(), repo, reference); err != nil {
		writeErr(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest "+reference+" not found in repository "+repo)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
