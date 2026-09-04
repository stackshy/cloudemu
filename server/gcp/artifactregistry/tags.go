// tags.go implements the packages.tags write operations (create, patch,
// delete) that subcollections.go left read-only, plus the enforcement of a
// Docker repository's dockerConfig.immutableTags flag. Real Artifact Registry
// blocks re-pointing or deleting a tag once immutable tags are enabled (create
// of a brand-new tag id is always allowed); this file is the only place that
// checks the flag, since createRepository/patchRepository merely persist it
// (see operations.go) without enforcing anything.
package artifactregistry

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// tagUnsetter is the optional GCP-only driver extension for removing a single
// tag without deleting the underlying package/version (packages.tags.delete).
// Only the GCP Artifact Registry mock implements it.
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
// tag (patch/delete) is blocked.
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

	if containsTagName(img.Tags, tagID) {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists", "tag "+tagID+" already exists")
		return
	}

	var body tagsJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if !checkTagVersion(w, rt, body.Version, img.Digest) {
		return
	}

	if err := h.registry.TagImage(r.Context(), rt.repository, img.Digest, tagID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toTagJSON(rt, tagID, img.Digest))
}

// patchTag implements packages.tags.patch. The mock's package/version model
// is one version per package, so a tag under package {pkg} only ever
// resolves to version {pkg}; a patch can therefore only confirm that version
// (a differing one 404s as not found), but the immutableTags gate below is
// still fully meaningful and is what real clients probe.
func (h *Handler) patchTag(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	if !containsTagName(img.Tags, rt.pkgSubID) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "tag "+rt.pkgSubID+" not found")
		return
	}

	var body tagsJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if body.Version != "" && !checkTagVersion(w, rt, body.Version, img.Digest) {
		return
	}

	if !h.rejectIfImmutable(w, r, rt, "updated") {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toTagJSON(rt, rt.pkgSubID, img.Digest))
}

// deleteTag implements packages.tags.delete, blocked by immutableTags.
func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	if !containsTagName(img.Tags, rt.pkgSubID) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "tag "+rt.pkgSubID+" not found")
		return
	}

	if !h.rejectIfImmutable(w, r, rt, "deleted") {
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

// rejectIfImmutable writes a FAILED_PRECONDITION response and returns false
// when the tag's repository has dockerConfig.immutableTags enabled; verb
// names the blocked action ("updated"/"deleted") in the error message.
func (h *Handler) rejectIfImmutable(w http.ResponseWriter, r *http.Request, rt *route, verb string) bool {
	repo, err := h.registry.GetRepository(r.Context(), rt.repository)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return false
	}

	if repo.Tags[immutableTagsTag] != trueTag {
		return true
	}

	gcprest.WriteCErr(w, cerrors.Newf(cerrors.FailedPrecondition,
		"repository %q has immutable tags; tag %q cannot be %s", rt.repository, rt.pkgSubID, verb))

	return false
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

// containsTagName reports whether tag is present in tags.
func containsTagName(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}

	return false
}
