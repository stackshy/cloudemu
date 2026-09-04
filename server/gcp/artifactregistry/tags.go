// tags.go implements the packages.tags write operations (create, patch,
// delete) that subcollections.go left read-only, plus the enforcement of a
// Docker repository's dockerConfig.immutableTags flag. Real Artifact Registry
// blocks re-pointing or deleting a tag once immutable tags are enabled (create
// of a brand-new tag id is always allowed). The existence checks and the
// immutableTags gate are evaluated atomically inside the provider's
// CreateTag/PatchTag/UntagImage (one lock acquisition each), not composed here
// from separate GetRepository/TagImage calls: a wire-layer check-then-act
// would leave a window for a concurrent repository patch to flip
// immutableTags, or for two concurrent creates of the same tag id to both
// succeed.
package artifactregistry

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// tagCreator is the optional GCP-only driver extension for atomically
// creating a new tag (packages.tags.create). Only the GCP Artifact Registry
// mock implements it.
type tagCreator interface {
	CreateTag(ctx context.Context, repository, digest, tag string) error
}

// tagPatcher is the optional GCP-only driver extension for atomically
// validating a tag patch (packages.tags.patch), including the immutableTags
// gate. Only the GCP Artifact Registry mock implements it.
type tagPatcher interface {
	PatchTag(ctx context.Context, repository, digest, tag string) error
}

// tagUnsetter is the optional GCP-only driver extension for atomically
// removing a single tag without deleting the underlying package/version
// (packages.tags.delete), including the immutableTags gate. Only the GCP
// Artifact Registry mock implements it.
type tagUnsetter interface {
	UntagImage(ctx context.Context, repository, digest, tag string) error
}

// serveTags dispatches the tags sub-collection: GET lists or gets a tag, POST
// creates one (tags.create), PATCH re-points an existing one (tags.patch),
// and DELETE removes one (tags.delete).
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.pkgSubID == "" {
		h.serveTagsCollection(w, r, rt)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTag(w, r, rt)
	case http.MethodPatch:
		h.patchTag(w, r, rt)
	case http.MethodDelete:
		h.deleteTag(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported tag operation")
	}
}

// serveTagsCollection handles the tags collection itself: GET lists, POST
// creates (tagId supplied as a query parameter, since the resource has no id
// yet).
func (h *Handler) serveTagsCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.listTags(w, r, rt)
	case http.MethodPost:
		h.createTag(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported tags operation")
	}
}

// createTag implements packages.tags.create. A brand-new tag id is always
// allowed, even on an immutable-tags repository; only mutating an existing
// tag (patch/delete) is blocked. The existence-check-then-write is delegated
// to the provider's CreateTag so it happens under one lock acquisition.
func (h *Handler) createTag(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	tagID := r.URL.Query().Get("tagId")
	if tagID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "tagId is required")
		return
	}

	var body tagsJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if !checkTagVersion(w, rt, body.Version, img.Digest) {
		return
	}

	creator, ok := h.registry.(tagCreator)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "tags.create unsupported")
		return
	}

	if err := creator.CreateTag(r.Context(), rt.repository, img.Digest, tagID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toTagJSON(rt, tagID, img.Digest))
}

// patchTag implements packages.tags.patch. The mock's package/version model
// is one version per package, so a tag under package {pkg} only ever
// resolves to version {pkg}; a patch can therefore only confirm that version
// (a differing one 404s as not found), but the immutableTags gate — checked
// atomically inside the provider's PatchTag — is still fully meaningful and
// is what real clients probe.
func (h *Handler) patchTag(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	var body tagsJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if body.Version != "" && !checkTagVersion(w, rt, body.Version, img.Digest) {
		return
	}

	patcher, ok := h.registry.(tagPatcher)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "tags.patch unsupported")
		return
	}

	if err := patcher.PatchTag(r.Context(), rt.repository, img.Digest, rt.pkgSubID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toTagJSON(rt, rt.pkgSubID, img.Digest))
}

// deleteTag implements packages.tags.delete. The existence check and the
// immutableTags gate are evaluated atomically inside the provider's
// UntagImage, under the same lock as the removal itself.
func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	unsetter, ok := h.registry.(tagUnsetter)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "tags.delete unsupported")
		return
	}

	if err := unsetter.UntagImage(r.Context(), rt.repository, img.Digest, rt.pkgSubID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// checkTagVersion validates that versionRef names an existing version under
// the same package (rt.pkg), i.e. the digest the package's single image
// carries. Writes a 404 and returns false otherwise.
func checkTagVersion(w http.ResponseWriter, rt *route, versionRef, wantDigest string) bool {
	if versionRef == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "version is required")
		return false
	}

	pkg, version, ok := parseVersionRef(versionRef)
	if !ok || pkg != rt.pkg || version != wantDigest {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "version "+versionRef+" not found")
		return false
	}

	return true
}

const (
	tagPackagesMarker = "/packages/"
	tagVersionsMarker = "/versions/"
)

// parseVersionRef extracts the package and version ids from a Tag.version
// resource name (".../packages/{pkg}/versions/{version}").
func parseVersionRef(ref string) (pkg, version string, ok bool) {
	pi := strings.Index(ref, tagPackagesMarker)
	vi := strings.Index(ref, tagVersionsMarker)

	if pi < 0 || vi < 0 || vi < pi {
		return "", "", false
	}

	pkg = ref[pi+len(tagPackagesMarker) : vi]
	version = ref[vi+len(tagVersionsMarker):]

	if pkg == "" || version == "" {
		return "", "", false
	}

	return pkg, version, true
}
