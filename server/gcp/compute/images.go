package compute

import (
	"context"
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

// gcpImageNameTag is the tag we round-trip the image name through.
const gcpImageNameTag = "cloudemu:gcpImageName"

type imageRequest struct {
	Name           string            `json:"name"`
	SourceDisk     string            `json:"sourceDisk,omitempty"`
	SourceSnapshot string            `json:"sourceSnapshot,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

type imageResponse struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	SelfLink string            `json:"selfLink"`
	Labels   map[string]string `json:"labels,omitempty"`
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

	// GCP images can be created from a disk, snapshot, or imported. The
	// driver's CreateImage takes an InstanceID — we fake one from any
	// existing instance just so the driver lets the create succeed.
	insts, err := h.compute.DescribeInstances(r.Context(), nil, nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	instanceID := ""
	if len(insts) > 0 {
		instanceID = insts[0].ID
	}

	if instanceID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"images mock requires at least one running instance to derive the image from")

		return
	}

	cfg := computedriver.ImageConfig{
		InstanceID:  instanceID,
		Name:        req.Name,
		Description: req.Name,
		Tags:        mergeImageTags(req.Labels, req.Name),
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

	return imageResponse{
		Kind:     "compute#image",
		ID:       numericID(img.ID),
		Name:     name,
		Status:   "READY",
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "images", name),
		Labels:   stripInternalImageTags(img.Tags),
	}
}

func mergeImageTags(in map[string]string, name string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[gcpImageNameTag] = name

	return out
}

func stripInternalImageTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == gcpImageNameTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
