package compute

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// gcpImageNameTag is the tag we round-trip the image name through.
const gcpImageNameTag = "cloudemu:gcpImageName"

// Internal tags round-tripping the GCP-specific image fields the portable
// compute driver model does not carry, so a read reflects them.
const (
	gcpImageSourceDiskTag     = "cloudemu:gcpImageSourceDisk"
	gcpImageSourceSnapshotTag = "cloudemu:gcpImageSourceSnapshot"
	gcpImageFamilyTag         = "cloudemu:gcpImageFamily"
	gcpImageDiskSizeGbTag     = "cloudemu:gcpImageDiskSizeGb"
)

type imageRequest struct {
	Name           string            `json:"name"`
	SourceDisk     string            `json:"sourceDisk,omitempty"`
	SourceSnapshot string            `json:"sourceSnapshot,omitempty"`
	Family         string            `json:"family,omitempty"`
	DiskSizeGb     int               `json:"diskSizeGb,string,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

type imageResponse struct {
	Kind              string            `json:"kind"`
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Status            string            `json:"status"`
	SelfLink          string            `json:"selfLink"`
	SourceDisk        string            `json:"sourceDisk,omitempty"`
	SourceDiskID      string            `json:"sourceDiskId,omitempty"`
	SourceSnapshot    string            `json:"sourceSnapshot,omitempty"`
	Family            string            `json:"family,omitempty"`
	DiskSizeGb        string            `json:"diskSizeGb,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
}

type imageListResponse struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Items    []imageResponse `json:"items"`
	SelfLink string          `json:"selfLink"`
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertImage(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeGlobal {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "images are global resources")
		return
	}

	var req imageRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "image name required")
		return
	}

	if _, err := findImageByName(r.Context(), h.compute, req.Name); conflictIfExists(w, err, "image "+req.Name+" already exists") {
		return
	}

	// GCP images are created from a disk, snapshot, or import — never from a
	// source instance (that is EC2's model). Pass an empty InstanceID so the
	// driver takes the source-based path; record the source in the description
	// so a read reflects what it was built from.
	cfg := computedriver.ImageConfig{
		Name:        req.Name,
		Description: imageSourceDescription(&req),
		Tags:        h.mergeImageTags(r.Context(), req.Labels, &req),
	}

	if _, err := h.compute.CreateImage(r.Context(), cfg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, gcprest.ScopeGlobal, "",
		"images", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getImage(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toImageResponse(img, rp, hostFromRequest(r)))
}

//nolint:gocritic,dupl // rp is a request-scoped value; list/delete shape is duplicate-by-design across resources
func (h *Handler) listImages(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	imgs, err := h.compute.DescribeImages(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	out := make([]imageResponse, 0, len(imgs))

	for i := range imgs {
		scope := rp
		scope.ResourceName = tagOr(imgs[i].Tags, gcpImageNameTag, imgs[i].Name)
		out = append(out, toImageResponse(&imgs[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, imageListResponse{
		Kind:     "compute#imageList",
		ID:       "projects/" + rp.Project + "/global/images",
		Items:    out,
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "images", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeregisterImage(r.Context(), img.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, gcprest.ScopeGlobal, "",
		"images", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// imageSourceDescription records the source the image was built from so a read
// reflects it. Falls back to the image name when no source was given (import).
func imageSourceDescription(req *imageRequest) string {
	switch {
	case req.SourceDisk != "":
		return "sourceDisk: " + req.SourceDisk
	case req.SourceSnapshot != "":
		return "sourceSnapshot: " + req.SourceSnapshot
	default:
		return req.Name
	}
}

func findImageByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.ImageInfo, error) {
	imgs, err := c.DescribeImages(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range imgs {
		if tagOr(imgs[i].Tags, gcpImageNameTag, imgs[i].Name) == name {
			return &imgs[i], nil
		}

		// Fall back to matching the driver-supplied Name field.
		if !strings.Contains(imgs[i].ID, "/") && imgs[i].Name == name {
			return &imgs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "image %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toImageResponse(img *computedriver.ImageInfo, rp gcprest.ResourcePath, host string) imageResponse {
	name := tagOr(img.Tags, gcpImageNameTag, img.Name)
	sourceDisk := img.Tags[gcpImageSourceDiskTag]

	resp := imageResponse{
		Kind:              "compute#image",
		ID:                numericID(img.ID),
		Name:              name,
		Status:            "READY",
		SelfLink:          gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "images", name),
		SourceDisk:        sourceDisk,
		SourceSnapshot:    img.Tags[gcpImageSourceSnapshotTag],
		Family:            img.Tags[gcpImageFamilyTag],
		DiskSizeGb:        img.Tags[gcpImageDiskSizeGbTag],
		Labels:            userLabels(img.Tags),
		CreationTimestamp: img.CreatedAt,
	}

	if sourceDisk != "" {
		resp.SourceDiskID = numericID(sourceDisk)
	}

	return resp
}

// mergeImageTags builds the image's tag map: user labels plus the internal tags
// round-tripping the GCP name and source fields (sourceDisk/sourceSnapshot/
// family/diskSizeGb). diskSizeGb is resolved from the source disk when the
// request does not carry it, mirroring real GCP.
func (h *Handler) mergeImageTags(ctx context.Context, in map[string]string, req *imageRequest) map[string]string {
	out := make(map[string]string, len(in)+internalTagCap)

	for k, v := range in {
		out[k] = v
	}

	out[gcpImageNameTag] = req.Name

	putIfSet(out, gcpImageSourceDiskTag, req.SourceDisk)
	putIfSet(out, gcpImageSourceSnapshotTag, req.SourceSnapshot)
	putIfSet(out, gcpImageFamilyTag, req.Family)

	if size := h.imageDiskSizeGb(ctx, req); size != "" {
		out[gcpImageDiskSizeGbTag] = size
	}

	return out
}

// imageDiskSizeGb returns the image's diskSizeGb as a string: the request value
// when given, otherwise the size of the source disk it is built from.
func (h *Handler) imageDiskSizeGb(ctx context.Context, req *imageRequest) string {
	if req.DiskSizeGb > 0 {
		return strconv.Itoa(req.DiskSizeGb)
	}

	if req.SourceDisk == "" {
		return ""
	}

	if vol, err := findDiskByName(ctx, h.compute, lastSegment(req.SourceDisk), zoneFromDiskURL(req.SourceDisk)); err == nil {
		return strconv.Itoa(vol.Size)
	}

	return ""
}

// zoneFromDiskURL extracts the zone from a zonal disk self-link of the form
// ".../zones/{zone}/disks/{name}". It returns "" when no zone segment is
// present, which matches a disk in any zone (defensive for bare names).
func zoneFromDiskURL(url string) string {
	segs := strings.Split(url, "/")
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == "zones" {
			return segs[i+1]
		}
	}

	return ""
}

// putIfSet inserts key=val into m only when val is non-empty.
func putIfSet(m map[string]string, key, val string) {
	if val != "" {
		m[key] = val
	}
}
