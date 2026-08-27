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

// gcpSnapshotNameTag is the tag we round-trip the snapshot name through.
const gcpSnapshotNameTag = "cloudemu:gcpSnapshotName"

// gcpSnapshotSourceDiskTag round-trips the caller's sourceDisk URL so a read
// echoes it verbatim (real GCP returns the source disk the caller passed,
// not our driver-internal disk id).
const gcpSnapshotSourceDiskTag = "cloudemu:gcpSnapshotSourceDisk"

// snapshotRequest mirrors the subset of compute#snapshot we accept.
type snapshotRequest struct {
	Name       string            `json:"name"`
	SourceDisk string            `json:"sourceDisk,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type snapshotResponse struct {
	Kind              string            `json:"kind"`
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	SourceDisk        string            `json:"sourceDisk,omitempty"`
	SourceDiskID      string            `json:"sourceDiskId,omitempty"`
	DiskSizeGb        string            `json:"diskSizeGb"`
	Status            string            `json:"status"`
	SelfLink          string            `json:"selfLink"`
	Labels            map[string]string `json:"labels,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
}

type snapshotListResponse struct {
	Kind     string             `json:"kind"`
	ID       string             `json:"id"`
	Items    []snapshotResponse `json:"items"`
	SelfLink string             `json:"selfLink"`
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertSnapshot(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeGlobal {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "snapshots are global resources")
		return
	}

	var req snapshotRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "snapshot name required")
		return
	}

	if _, err := findSnapshotByName(r.Context(), h.compute, req.Name); conflictIfExists(w, err, "snapshot "+req.Name+" already exists") {
		return
	}

	driverDiskID, err := h.resolveSourceDiskID(r.Context(), req.SourceDisk)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	cfg := computedriver.SnapshotConfig{
		VolumeID:    driverDiskID,
		Description: req.Name,
		Tags:        mergeSnapshotTags(req.Labels, req.Name, req.SourceDisk),
	}

	if _, err := h.compute.CreateSnapshot(r.Context(), cfg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, gcprest.ScopeGlobal, "",
		"snapshots", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getSnapshot(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	snap, err := findSnapshotByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSnapshotResponse(snap, rp, hostFromRequest(r)))
}

//nolint:gocritic,dupl // rp is a request-scoped value; list/delete shape is duplicate-by-design across resources
func (h *Handler) listSnapshots(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	snaps, err := h.compute.DescribeSnapshots(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	out := make([]snapshotResponse, 0, len(snaps))

	for i := range snaps {
		scope := rp
		scope.ResourceName = tagOr(snaps[i].Tags, gcpSnapshotNameTag, snaps[i].ID)
		out = append(out, toSnapshotResponse(&snaps[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, snapshotListResponse{
		Kind:     "compute#snapshotList",
		ID:       "projects/" + rp.Project + "/global/snapshots",
		Items:    out,
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "snapshots", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteSnapshot(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	snap, err := findSnapshotByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeleteSnapshot(r.Context(), snap.ID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, gcprest.ScopeGlobal, "",
		"snapshots", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

func findSnapshotByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.SnapshotInfo, error) {
	snaps, err := c.DescribeSnapshots(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range snaps {
		if tagOr(snaps[i].Tags, gcpSnapshotNameTag, "") == name {
			return &snaps[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "snapshot %s not found", name)
}

// resolveSourceDiskID maps an SDK-supplied sourceDisk URL (a self-link of a
// disk) to the driver-internal volume ID by matching on the disk-name tag.
func (h *Handler) resolveSourceDiskID(ctx context.Context, sourceDisk string) (string, error) {
	if sourceDisk == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "sourceDisk is required")
	}

	idx := strings.LastIndex(sourceDisk, "/disks/")
	if idx < 0 {
		return sourceDisk, nil
	}

	name := sourceDisk[idx+len("/disks/"):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}

	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return "", err
	}

	for i := range vols {
		if tagOr(vols[i].Tags, gcpDiskNameTag, "") == name {
			return vols[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "source disk %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toSnapshotResponse(snap *computedriver.SnapshotInfo, rp gcprest.ResourcePath, host string) snapshotResponse {
	name := tagOr(snap.Tags, gcpSnapshotNameTag, rp.ResourceName)

	// Echo the caller's sourceDisk URL when recorded; fall back to the
	// driver-internal disk id for snapshots created directly on the driver.
	sourceDisk := tagOr(snap.Tags, gcpSnapshotSourceDiskTag, snap.VolumeID)

	resp := snapshotResponse{
		Kind:              "compute#snapshot",
		ID:                numericID(snap.ID),
		Name:              name,
		SourceDisk:        sourceDisk,
		DiskSizeGb:        strconv.Itoa(snap.Size),
		Status:            "READY",
		SelfLink:          gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "snapshots", name),
		Labels:            userLabels(snap.Tags),
		CreationTimestamp: snap.CreatedAt,
	}

	if snap.VolumeID != "" {
		resp.SourceDiskID = numericID(snap.VolumeID)
	}

	return resp
}

func mergeSnapshotTags(in map[string]string, name, sourceDisk string) map[string]string {
	out := make(map[string]string, len(in)+internalTagCap)

	for k, v := range in {
		out[k] = v
	}

	out[gcpSnapshotNameTag] = name

	if sourceDisk != "" {
		out[gcpSnapshotSourceDiskTag] = sourceDisk
	}

	return out
}
