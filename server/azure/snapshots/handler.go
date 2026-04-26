// Package snapshots serves Azure ARM Microsoft.Compute/snapshots requests.
package snapshots

import (
	"context"
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// diskARMNameTag mirrors the constant in server/azure/disks. We resolve
// snapshot source disks by ARM name → driver volume by listing volumes and
// matching this tag, since the driver doesn't index by ARM name natively.
const diskARMNameTag = "cloudemu:azureDiskName"

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "snapshots"
	armNameTag      = "cloudemu:azureSnapshotName"
	defaultLocation = "eastus"
)

// Handler serves Microsoft.Compute/snapshots requests.
type Handler struct {
	compute computedriver.Compute
}

// New returns a snapshots handler. The driver's Snapshot* methods provide
// the underlying storage.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

// Matches returns true for ARM snapshots paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

// ServeHTTP routes the request based on method and path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.serveCollection(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, rp)
	case http.MethodGet:
		h.get(w, r, rp)
	case http.MethodDelete:
		h.delete(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)

		return
	}

	snaps, err := h.compute.DescribeSnapshots(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]snapshotResponse, 0, len(snaps))

	for i := range snaps {
		name := tagOr(snaps[i].Tags, armNameTag, snaps[i].ID)
		scope := rp
		scope.ResourceName = name
		out = append(out, toSnapshotResponse(&snaps[i], scope, ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, snapshotListResponse{Value: out})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req snapshotRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	driverVolID, err := h.resolveSourceVolumeID(r.Context(), sourceVolumeID(req.Properties.CreationData))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	cfg := computedriver.SnapshotConfig{
		VolumeID:    driverVolID,
		Description: rp.ResourceName,
		Tags:        mergeSnapshotTags(req.Tags, rp.ResourceName),
	}

	snap, err := h.compute.CreateSnapshot(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	body := toSnapshotResponse(snap, rp, location)

	writeSnapshotAsync(w, r, rp.Subscription, "snap-create-"+rp.ResourceName, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	snap, err := findSnapshotByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSnapshotResponse(snap, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	snap, err := findSnapshotByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeleteSnapshot(r.Context(), snap.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeSnapshotAsync(w, r, rp.Subscription, "snap-delete-"+rp.ResourceName, nil)
}

func findSnapshotByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.SnapshotInfo, error) {
	snaps, err := c.DescribeSnapshots(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range snaps {
		if tagOr(snaps[i].Tags, armNameTag, "") == name {
			return &snaps[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "snapshot %s not found", name)
}

// writeSnapshotAsync replies 202 + Azure-AsyncOperation header.
func writeSnapshotAsync(w http.ResponseWriter, r *http.Request, sub, opID string, body any) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	statusURL := scheme + "://" + r.Host +
		"/subscriptions/" + sub +
		"/providers/Microsoft.Compute/locations/eastus/operationStatuses/" + opID +
		"?api-version=2023-09-01"

	w.Header().Set("Azure-AsyncOperation", statusURL)
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "0")

	if body != nil {
		azurearm.WriteJSON(w, http.StatusAccepted, body)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

//nolint:gocritic // rp is a request-scoped value
func toSnapshotResponse(snap *computedriver.SnapshotInfo, rp azurearm.ResourcePath, location string) snapshotResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(snap.Tags, armNameTag, rp.ResourceName)

	return snapshotResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Tags:     stripInternalTags(snap.Tags),
		Properties: snapshotResponseProps{
			ProvisioningState: "Succeeded",
			DiskSizeGB:        snap.Size,
			DiskState:         "ReadyToUpload",
			CreationData: &creationData{
				CreateOption:     "Copy",
				SourceResourceID: snap.VolumeID,
			},
		},
	}
}

func sourceVolumeID(c *creationData) string {
	if c == nil {
		return ""
	}

	if c.SourceResourceID != "" {
		return c.SourceResourceID
	}

	return c.SourceURI
}

// resolveSourceVolumeID maps a user-supplied SourceResourceID (an ARM disk
// path like /subscriptions/.../disks/{name}) to the internal driver volume
// ID that CreateSnapshot expects. Returns NotFound if no disk matches.
//
// If src isn't an ARM disk path we pass it through unchanged — callers may
// already be supplying a driver-internal ID.
func (h *Handler) resolveSourceVolumeID(ctx context.Context, src string) (string, error) {
	if src == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "creationData.sourceResourceId is required")
	}

	// Extract the disk name from a "/disks/{name}" suffix.
	idx := strings.LastIndex(src, "/disks/")
	if idx < 0 {
		return src, nil
	}

	name := src[idx+len("/disks/"):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}

	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return "", err
	}

	for i := range vols {
		if tagOr(vols[i].Tags, diskARMNameTag, "") == name {
			return vols[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "source disk %s not found", name)
}

func mergeSnapshotTags(in map[string]string, name string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = name

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

func stripInternalTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == armNameTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
