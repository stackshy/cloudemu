package artifactregistry

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// The driver models docker images by digest without a distinct image-name
// dimension, so each image is surfaced as one AR package (id = digest) with a
// single version (the digest) and its docker tags. This keeps the packages /
// versions / tags / files sub-collections correctly shaped and populated from
// real repository state instead of the previous behavior of silently returning
// the Repository body.

// servePackages dispatches the packages sub-tree: the packages collection, a
// single package, and its versions / tags sub-collections.
func (h *Handler) servePackages(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.pkg == "" {
		h.listPackages(w, r, rt)
		return
	}

	switch rt.pkgSub {
	case versionsSeg:
		if rt.pkgSubID != "" {
			h.getVersion(w, r, rt)
			return
		}

		h.listVersions(w, r, rt)
	case tagsSeg:
		if rt.pkgSubID != "" {
			h.getTag(w, r, rt)
			return
		}

		h.listTags(w, r, rt)
	case "":
		h.getPackage(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported package sub-collection "+rt.pkgSub)
	}
}

// getVersion returns a single version (.../packages/{pkg}/versions/{version}).
// The driver models one version per image, keyed by the digest, so the version
// id must equal the package's digest.
func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	if rt.pkgSubID != img.Digest {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "version "+rt.pkgSubID+" not found")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt, img))
}

// getTag returns a single tag (.../packages/{pkg}/tags/{tag}).
func (h *Handler) getTag(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	for _, t := range img.Tags {
		if t == rt.pkgSubID {
			gcprest.WriteJSON(w, http.StatusOK, toTagJSON(rt, t, img.Digest))
			return
		}
	}

	gcprest.WriteError(w, http.StatusNotFound, "notFound", "tag "+rt.pkgSubID+" not found")
}

func (h *Handler) repoImages(w http.ResponseWriter, r *http.Request, rt *route) ([]crdriver.ImageDetail, bool) {
	images, err := h.registry.ListImages(r.Context(), rt.repository)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return nil, false
	}

	return images, true
}

func (h *Handler) listPackages(w http.ResponseWriter, r *http.Request, rt *route) {
	out, token, ok := pagedImageList(h, w, r, rt, toPackageJSON)
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, listPackagesResponse{Packages: out, NextPageToken: token})
}

// pagedImageList lists a repository's images, paginates them, and converts each
// to T. Shared by the packages and files sub-collections; ok is false when it
// has already written an error response.
func pagedImageList[T any](
	h *Handler, w http.ResponseWriter, r *http.Request, rt *route,
	conv func(*route, *crdriver.ImageDetail) T,
) (items []T, nextPageToken string, ok bool) {
	images, ok := h.repoImages(w, r, rt)
	if !ok {
		return nil, "", false
	}

	page, ok := paginateImages(w, r, images)
	if !ok {
		return nil, "", false
	}

	out := make([]T, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, conv(rt, &page.Items[i]))
	}

	return out, page.NextPageToken, true
}

func (h *Handler) getPackage(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPackageJSON(rt, img))
}

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, listVersionsResponse{Versions: []versionJSON{toVersionJSON(rt, img)}})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, rt *route) {
	img, ok := h.findImage(w, r, rt)
	if !ok {
		return
	}

	out := make([]tagsJSON, 0, len(img.Tags))
	for _, t := range img.Tags {
		out = append(out, toTagJSON(rt, t, img.Digest))
	}

	gcprest.WriteJSON(w, http.StatusOK, listTagsResponse{Tags: out})
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request, rt *route) {
	out, token, ok := pagedImageList(h, w, r, rt, toFileJSON)
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, listFilesResponse{Files: out, NextPageToken: token})
}

// findImage resolves rt.pkg (a digest) to its image, 404ing when absent.
func (h *Handler) findImage(w http.ResponseWriter, r *http.Request, rt *route) (*crdriver.ImageDetail, bool) {
	images, ok := h.repoImages(w, r, rt)
	if !ok {
		return nil, false
	}

	for i := range images {
		if images[i].Digest == rt.pkg {
			return &images[i], true
		}
	}

	gcprest.WriteError(w, http.StatusNotFound, "notFound", "package "+rt.pkg+" not found")

	return nil, false
}

func packageResourceName(rt *route, pkg string) string {
	return repositoryResourceName(rt.project, rt.location, rt.repository) + "/packages/" + pkg
}

func toPackageJSON(rt *route, d *crdriver.ImageDetail) packageJSON {
	return packageJSON{
		Name:        packageResourceName(rt, d.Digest),
		DisplayName: d.Digest,
		CreateTime:  d.PushedAt,
		UpdateTime:  d.PushedAt,
	}
}

func toVersionJSON(rt *route, d *crdriver.ImageDetail) versionJSON {
	related := make([]tagsJSON, 0, len(d.Tags))
	for _, t := range d.Tags {
		related = append(related, toTagJSON(rt, t, d.Digest))
	}

	return versionJSON{
		Name:        packageResourceName(rt, d.Digest) + "/versions/" + d.Digest,
		CreateTime:  d.PushedAt,
		UpdateTime:  d.PushedAt,
		RelatedTags: related,
	}
}

func toTagJSON(rt *route, tag, digest string) tagsJSON {
	return tagsJSON{
		Name:    packageResourceName(rt, digest) + "/tags/" + tag,
		Version: packageResourceName(rt, digest) + "/versions/" + digest,
	}
}

func toFileJSON(rt *route, d *crdriver.ImageDetail) fileJSON {
	return fileJSON{
		Name:       repositoryResourceName(rt.project, rt.location, rt.repository) + "/files/" + d.Digest,
		SizeBytes:  strconv.FormatInt(d.SizeBytes, 10),
		Owner:      packageResourceName(rt, d.Digest) + "/versions/" + d.Digest,
		CreateTime: d.PushedAt,
		UpdateTime: d.PushedAt,
	}
}
