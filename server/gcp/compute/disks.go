package compute

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

// gcpDiskNameTag is the tag key used to round-trip the GCP disk name through
// the underlying compute driver, since the driver indexes by its own ID.
const gcpDiskNameTag = "cloudemu:gcpDiskName"

// diskRequest mirrors the subset of GCP compute#disk we accept on insert.
type diskRequest struct {
	Name        string            `json:"name"`
	SizeGb      int               `json:"sizeGb,string,omitempty"`
	SizeGbInt   int               `json:"-"`
	Type        string            `json:"type,omitempty"`
	SourceImage string            `json:"sourceImage,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// diskResponse mirrors the subset of compute#disk we return.
type diskResponse struct {
	Kind              string            `json:"kind"`
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	SizeGb            string            `json:"sizeGb"`
	Type              string            `json:"type"`
	Status            string            `json:"status"`
	Zone              string            `json:"zone"`
	SelfLink          string            `json:"selfLink"`
	Labels            map[string]string `json:"labels,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
}

type diskListResponse struct {
	Kind     string         `json:"kind"`
	ID       string         `json:"id"`
	Items    []diskResponse `json:"items"`
	SelfLink string         `json:"selfLink"`
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertDisk(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeZones {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "disks must be created in a zone")
		return
	}

	var req diskRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "disk name required")
		return
	}

	cfg := computedriver.VolumeConfig{
		Size:             pickSize(req.SizeGb, req.SizeGbInt),
		VolumeType:       lastSegment(req.Type),
		AvailabilityZone: rp.ScopeName,
		Tags:             mergeDiskTags(req.Labels, req.Name),
	}

	_, err := h.compute.CreateVolume(r.Context(), cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"disks", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getDisk(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toDiskResponse(vol, rp, hostFromRequest(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listDisks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	vols, err := h.compute.DescribeVolumes(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	out := make([]diskResponse, 0, len(vols))

	for i := range vols {
		scope := rp
		scope.ResourceName = tagOr(vols[i].Tags, gcpDiskNameTag, vols[i].ID)
		out = append(out, toDiskResponse(&vols[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, diskListResponse{
		Kind:     "compute#diskList",
		ID:       "projects/" + rp.Project + "/zones/" + rp.ScopeName + "/disks",
		Items:    out,
		SelfLink: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "disks", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteDisk(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeleteVolume(r.Context(), vol.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"disks", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

func findDiskByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.VolumeInfo, error) {
	vols, err := c.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range vols {
		if tagOr(vols[i].Tags, gcpDiskNameTag, "") == name {
			return &vols[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "disk %s not found", name)
}

// diskStatusReady is the only status we report; the underlying driver doesn't
// model the GCP-specific CREATING / FAILED / DELETING transitions.
const diskStatusReady = "READY"

// toDiskResponse maps a driver VolumeInfo to GCP REST disk JSON.
//
//nolint:gocritic // rp is a request-scoped value
func toDiskResponse(vol *computedriver.VolumeInfo, rp gcprest.ResourcePath, host string) diskResponse {
	name := tagOr(vol.Tags, gcpDiskNameTag, rp.ResourceName)

	return diskResponse{
		Kind:     "compute#disk",
		ID:       numericID(vol.ID),
		Name:     name,
		SizeGb:   strconv.Itoa(vol.Size),
		Type:     gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "diskTypes", defaultDiskType(vol.VolumeType)),
		Status:   diskStatusReady,
		Zone:     host + "/compute/v1/projects/" + rp.Project + "/zones/" + rp.ScopeName,
		SelfLink: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "disks", name),
		Labels:   stripInternalDiskTags(vol.Tags),
	}
}

func defaultDiskType(vt string) string {
	if vt == "" {
		return "pd-standard"
	}

	return vt
}

func mergeDiskTags(in map[string]string, name string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[gcpDiskNameTag] = name

	return out
}

func stripInternalDiskTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == gcpDiskNameTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// pickSize chooses sizeGb from the alternate fields the SDK might use.
func pickSize(sFromString, sInt int) int {
	if sFromString > 0 {
		return sFromString
	}

	return sInt
}

// lastSegment returns the trailing path segment of a self-link or full URL.
// Disk types arrive as ".../diskTypes/pd-ssd" — the driver wants just "pd-ssd".
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}

	return s
}
