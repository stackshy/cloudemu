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

// gcpDiskNameTag is the tag key used to round-trip the GCP disk name through
// the underlying compute driver, since the driver indexes by its own ID.
const gcpDiskNameTag = "cloudemu:gcpDiskName"

// gcpDiskSourceImageTag round-trips the sourceImage URL the caller created the
// disk from, so a read echoes it (real GCP retains sourceImage/sourceImageId).
const gcpDiskSourceImageTag = "cloudemu:gcpDiskSourceImage"

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
	SourceImage       string            `json:"sourceImage,omitempty"`
	SourceImageID     string            `json:"sourceImageId,omitempty"`
	Users             []string          `json:"users,omitempty"`
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

	if _, err := findDiskByName(r.Context(), h.compute, req.Name); conflictIfExists(w, err, "disk "+req.Name+" already exists") {
		return
	}

	cfg := computedriver.VolumeConfig{
		Size:             pickSize(req.SizeGb, req.SizeGbInt),
		VolumeType:       lastSegment(req.Type),
		AvailabilityZone: rp.ScopeName,
		Tags:             mergeDiskTags(req.Labels, req.Name, req.SourceImage),
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

	host := hostFromRequest(r)
	users := h.diskUsersByName(r.Context(), host, rp.Project)

	gcprest.WriteJSON(w, http.StatusOK, toDiskResponse(vol, rp, host, users[rp.ResourceName]))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listDisks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	vols, err := h.compute.DescribeVolumes(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	users := h.diskUsersByName(r.Context(), host, rp.Project)
	out := make([]diskResponse, 0, len(vols))

	for i := range vols {
		scope := rp
		name := tagOr(vols[i].Tags, gcpDiskNameTag, vols[i].ID)
		scope.ResourceName = name
		out = append(out, toDiskResponse(&vols[i], scope, host, users[name]))
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

// diskResizeRequest is the compute#disksResizeRequest body (sizeGb arrives as a
// JSON string from the protobuf clients).
type diskResizeRequest struct {
	SizeGb    int `json:"sizeGb,string,omitempty"`
	SizeGbInt int `json:"-"`
}

// volumeResizer is the GCP-local grow-a-disk capability the GCE Mock implements;
// reached via a type assertion so the shared compute driver stays unchanged.
type volumeResizer interface {
	ResizeVolumeGCP(volumeID string, sizeGb int) error
}

// resizeDisk handles POST .../disks/{name}/resize, growing the disk to the
// requested size and returning a DONE Operation (GCP forbids shrinking).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) resizeDisk(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var req diskResizeRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	newSize := pickSize(req.SizeGb, req.SizeGbInt)
	if newSize < vol.Size {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "disk size cannot be reduced")
		return
	}

	resizer, ok := h.compute.(volumeResizer)
	if !ok {
		writeNotImplemented(w, "disk resize")
		return
	}

	if err := resizer.ResizeVolumeGCP(vol.ID, newSize); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"disks", rp.ResourceName, "resize")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// disksScopedList is one zone's bucket in an aggregated disk list.
type disksScopedList struct {
	Disks   []diskResponse     `json:"disks,omitempty"`
	Warning *scopedListWarning `json:"warning,omitempty"`
}

type diskAggregatedListResponse struct {
	Kind     string                     `json:"kind"`
	ID       string                     `json:"id"`
	Items    map[string]disksScopedList `json:"items"`
	SelfLink string                     `json:"selfLink"`
}

// aggregatedListDisks handles GET /aggregated/disks, returning every disk
// grouped by its "zones/{zone}" scope.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListDisks(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	vols, err := h.compute.DescribeVolumes(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	users := h.diskUsersByName(r.Context(), host, rp.Project)
	items := make(map[string]disksScopedList)

	for i := range vols {
		zone := vols[i].AvailabilityZone
		name := tagOr(vols[i].Tags, gcpDiskNameTag, vols[i].ID)
		scope := gcprest.ResourcePath{
			Project: rp.Project, Scope: gcprest.ScopeZones, ScopeName: zone, ResourceName: name,
		}
		key := "zones/" + zone
		bucket := items[key]
		bucket.Disks = append(bucket.Disks, toDiskResponse(&vols[i], scope, host, users[name]))
		items[key] = bucket
	}

	gcprest.WriteJSON(w, http.StatusOK, diskAggregatedListResponse{
		Kind:     "compute#diskAggregatedList",
		ID:       "projects/" + rp.Project + "/aggregated/disks",
		Items:    items,
		SelfLink: strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + rp.Project + "/aggregated/disks",
	})
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

// toDiskResponse maps a driver VolumeInfo to GCP REST disk JSON. users is the
// list of instance self-links the disk is attached to (empty when detached).
//
//nolint:gocritic // rp is a request-scoped value
func toDiskResponse(vol *computedriver.VolumeInfo, rp gcprest.ResourcePath, host string, users []string) diskResponse {
	name := tagOr(vol.Tags, gcpDiskNameTag, rp.ResourceName)
	sourceImage := vol.Tags[gcpDiskSourceImageTag]

	resp := diskResponse{
		Kind:              "compute#disk",
		ID:                numericID(vol.ID),
		Name:              name,
		SizeGb:            strconv.Itoa(vol.Size),
		Type:              gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "diskTypes", defaultDiskType(vol.VolumeType)),
		Status:            diskStatusReady,
		Zone:              host + "/compute/v1/projects/" + rp.Project + "/zones/" + rp.ScopeName,
		SelfLink:          gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "disks", name),
		SourceImage:       sourceImage,
		Users:             users,
		Labels:            userLabels(vol.Tags),
		CreationTimestamp: vol.CreatedAt,
	}

	if sourceImage != "" {
		resp.SourceImageID = numericID(sourceImage)
	}

	return resp
}

// diskUsersByName scans instances and maps each disk name to the self-links of
// the instances it is attached to, so a disk read reflects its users[] (kept
// consistent with the instance-side disks[] populated by attachDisk).
func (h *Handler) diskUsersByName(ctx context.Context, host, project string) map[string][]string {
	instances, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil
	}

	out := make(map[string][]string)

	for i := range instances {
		instName := tagOr(instances[i].Tags, gcpNameTag, "")
		zone := tagOr(instances[i].Tags, keyZone, "")
		link := gcprest.SelfLink(host, project, gcprest.ScopeZones, zone, "instances", instName)

		attached := decodeDisks(instances[i].Tags)
		for j := range attached {
			if dn := lastSegment(attached[j].Source); dn != "" {
				out[dn] = append(out[dn], link)
			}
		}
	}

	return out
}

func defaultDiskType(vt string) string {
	if vt == "" {
		return "pd-standard"
	}

	return vt
}

func mergeDiskTags(in map[string]string, name, sourceImage string) map[string]string {
	out := make(map[string]string, len(in)+internalTagCap)

	for k, v := range in {
		out[k] = v
	}

	out[gcpDiskNameTag] = name

	if sourceImage != "" {
		out[gcpDiskSourceImageTag] = sourceImage
	}

	return out
}

// conflictIfExists writes a 409 alreadyExists (or the underlying error) and
// returns true when a name-existence probe found the resource or errored; it
// returns false, letting the caller proceed, only when findErr is NotFound.
func conflictIfExists(w http.ResponseWriter, findErr error, msg string) bool {
	switch {
	case findErr == nil:
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists", msg)
		return true
	case !cerrors.IsNotFound(findErr):
		gcprest.WriteCErr(w, findErr)
		return true
	default:
		return false
	}
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
