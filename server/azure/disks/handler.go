// Package disks serves Azure ARM Microsoft.Compute/disks requests against a
// CloudEmu compute driver's volume operations.
//
// Supported operations:
//
//	PUT    .../disks/{name}  — CreateOrUpdate (returns 202 + Azure-AsyncOperation)
//	GET    .../disks/{name}  — Get
//	GET    .../disks         — List in resource group
//	DELETE .../disks/{name}  — Delete (returns 202 + Azure-AsyncOperation)
package disks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "disks"
	armNameTag      = "cloudemu:azureDiskName"
	rgTag           = "cloudemu:azureRG"
	createOptionTag = "cloudemu:createOption"
	sourceIDTag     = "cloudemu:sourceResourceId"
	defaultLocation = "eastus"

	// createOptionEmpty is the default disk createOption (an empty disk).
	createOptionEmpty = "Empty"

	// snapshotARMNameTag mirrors the ARM-name tag the snapshots handler stamps,
	// so a Copy source snapshot can be resolved to its driver volume.
	snapshotARMNameTag = "cloudemu:azureSnapshotName"

	// accessLevelRead is the default disk SAS access level when none is given.
	accessLevelRead = "Read"

	// defaultAccessDuration is the SAS lifetime (seconds) applied when the
	// request omits or zeroes durationInSeconds (Azure's own default is 3600).
	defaultAccessDuration = 3600
)

// Handler serves Microsoft.Compute/disks requests.
type Handler struct {
	compute computedriver.Compute
}

// New returns a disks handler backed by c. The underlying driver's volume
// methods (CreateVolume, DescribeVolumes, DeleteVolume) provide the storage.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

// Matches returns true for ARM disks paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

// ServeHTTP routes the request based on method and presence of a name segment.
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

	// SAS export/import actions are POST sub-resources on a named disk.
	if rp.SubResource != "" {
		h.serveAction(w, r, rp)
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

// serveAction dispatches the POST SAS actions on a named disk: beginGetAccess
// (grant a time-bounded SAS URI) and endGetAccess (revoke it).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"method not allowed: "+r.Method+" "+r.URL.Path)

		return
	}

	switch strings.ToLower(rp.SubResource) {
	case "begingetaccess":
		h.beginGetAccess(w, r, rp)
	case "endgetaccess":
		h.endGetAccess(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: action "+rp.SubResource)
	}
}

// beginGetAccess handles POST .../disks/{name}/beginGetAccess. It issues a
// time-bounded SAS URI for exporting/importing the disk's contents, returning
// the AccessUri (accessSAS) real Azure returns.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) beginGetAccess(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	accessor, ok := h.compute.(computedriver.AzureDiskAccessor)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "disk access SAS not supported")
		return
	}

	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req grantAccessData

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	access := req.Access
	if access == "" {
		access = accessLevelRead
	}

	duration := req.DurationInSeconds
	if duration <= 0 {
		duration = defaultAccessDuration
	}

	sas, err := accessor.GrantDiskAccess(r.Context(), vol.ID, access, duration)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, accessURIResponse{AccessSAS: sas})
}

// endGetAccess handles POST .../disks/{name}/endGetAccess, revoking any SAS
// access previously granted to the disk.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) endGetAccess(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	accessor, ok := h.compute.(computedriver.AzureDiskAccessor)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "disk access SAS not supported")
		return
	}

	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := accessor.RevokeDiskAccess(r.Context(), vol.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)

		return
	}

	vols, err := h.compute.DescribeVolumes(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]diskResponse, 0, len(vols))

	for i := range vols {
		if rp.ResourceGroup != "" && tagOr(vols[i].Tags, rgTag, "") != rp.ResourceGroup {
			continue
		}

		name := tagOr(vols[i].Tags, armNameTag, vols[i].ID)
		scope := rp
		scope.ResourceName = name
		out = append(out, toDiskResponse(&vols[i], scope, ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, diskListResponse{Value: out})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req diskRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	createOption, sourceID := creationParams(req.Properties.CreationData)

	size := req.Properties.DiskSizeGB
	if size == 0 && sourceID != "" {
		size = h.sourceSize(r.Context(), sourceID)
	}

	cfg := computedriver.VolumeConfig{
		Size:       size,
		VolumeType: skuName(req.SKU),
		IOPS:       req.Properties.DiskIOPSReadWrite,
		Throughput: req.Properties.DiskMBpsReadWrite,
		Tier:       diskTier(req.Properties.Tier, skuTier(req.SKU)),
		Tags:       mergeDiskTags(req.Tags, rp.ResourceName, rp.ResourceGroup, createOption, sourceID),
	}

	// ARM CreateOrUpdate is idempotent: replace an existing disk in place
	// rather than accumulating a duplicate under the same name.
	if existing, err := findDiskByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); err == nil {
		_ = h.compute.DeleteVolume(r.Context(), existing.ID)
	}

	vol, err := h.compute.CreateVolume(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	body := toDiskResponse(vol, rp, location)
	body.SKU = req.SKU

	writeDiskAsync(w, r, rp.Subscription, "disk-create-"+rp.ResourceName, body)
}

// creationParams extracts the createOption and source resource id from the
// request's creationData, defaulting createOption to "Empty" when unset.
func creationParams(c *creationData) (createOption, sourceID string) {
	if c == nil {
		return createOptionEmpty, ""
	}

	opt := c.CreateOption
	if opt == "" {
		opt = createOptionEmpty
	}

	src := c.SourceResourceID
	if src == "" {
		src = c.SourceURI
	}

	return opt, src
}

// sourceSize resolves the size (GB) of a Copy/Restore source referenced by an
// ARM snapshot or disk id, so a disk created from a source without an explicit
// diskSizeGB inherits the source's size. Returns 0 when unresolved.
func (h *Handler) sourceSize(ctx context.Context, sourceID string) int {
	if name, ok := armName(sourceID, "/snapshots/"); ok {
		snaps, err := h.compute.DescribeSnapshots(ctx, nil)
		if err == nil {
			for i := range snaps {
				if tagOr(snaps[i].Tags, snapshotARMNameTag, "") == name {
					return snaps[i].Size
				}
			}
		}
	}

	if name, ok := armName(sourceID, "/disks/"); ok {
		vols, err := h.compute.DescribeVolumes(ctx, nil)
		if err == nil {
			for i := range vols {
				if tagOr(vols[i].Tags, armNameTag, "") == name {
					return vols[i].Size
				}
			}
		}
	}

	return 0
}

// armName extracts the trailing resource name from an ARM id after the given
// "/{type}/" segment (e.g. "/snapshots/"). Reports false when absent.
func armName(id, segment string) (string, bool) {
	idx := strings.LastIndex(id, segment)
	if idx < 0 {
		return "", false
	}

	name := id[idx+len(segment):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}

	return name, name != ""
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toDiskResponse(vol, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	vol, err := findDiskByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeleteVolume(r.Context(), vol.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDiskAsync(w, r, rp.Subscription, "disk-delete-"+rp.ResourceName, nil)
}

// findDiskByName looks up a volume by its ARM-tagged name.
func findDiskByName(ctx context.Context, c computedriver.Compute, resourceGroup, name string) (*computedriver.VolumeInfo, error) {
	vols, err := c.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range vols {
		if tagOr(vols[i].Tags, armNameTag, "") != name {
			continue
		}

		// A resource's identity in ARM is {subscription, resourceGroup, name};
		// a same-named disk in another RG is a different resource.
		if resourceGroup != "" && tagOr(vols[i].Tags, rgTag, "") != resourceGroup {
			continue
		}

		return &vols[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "disk %s not found", name)
}

// writeDiskAsync replies 202 + Azure-AsyncOperation header. Optionally
// returns a JSON body for ops that the SDK reads (e.g. CreateOrUpdate body).
func writeDiskAsync(w http.ResponseWriter, r *http.Request, sub, opID string, body any) {
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

// toDiskResponse maps a driver VolumeInfo to an ARM disk JSON body.
//
//nolint:gocritic // rp is a request-scoped value
func toDiskResponse(vol *computedriver.VolumeInfo, rp azurearm.ResourcePath, location string) diskResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(vol.Tags, armNameTag, rp.ResourceName)

	var sku *diskSKU
	if vol.VolumeType != "" || vol.Tier != "" {
		sku = &diskSKU{Name: vol.VolumeType, Tier: vol.Tier}
	}

	createOption := tagOr(vol.Tags, createOptionTag, createOptionEmpty)
	cd := &creationData{CreateOption: createOption}

	if src := tagOr(vol.Tags, sourceIDTag, ""); src != "" {
		cd.SourceResourceID = src
	}

	return diskResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: location,
		SKU:      sku,
		Tags:     stripInternalDiskTags(vol.Tags),
		Properties: diskResponseProps{
			ProvisioningState: "Succeeded",
			DiskSizeGB:        vol.Size,
			DiskState:         diskStateFor(vol.State),
			CreationData:      cd,
			DiskIOPSReadWrite: vol.IOPS,
			DiskMBpsReadWrite: vol.Throughput,
			Tier:              vol.Tier,
			TimeCreated:       vol.CreatedAt,
			UniqueID:          diskUniqueID(vol.ID),
		},
	}
}

// diskUniqueID derives a stable GUID-shaped uniqueId from the driver volume id,
// mirroring the properties.uniqueId Azure assigns each managed disk.
func diskUniqueID(volID string) string {
	sum := sha256.Sum256([]byte(volID))
	h := hex.EncodeToString(sum[:])

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// diskTier prefers properties.tier, falling back to sku.tier.
func diskTier(propTier, skuTier string) string {
	if propTier != "" {
		return propTier
	}

	return skuTier
}

// ARM disk states we expose. Real Azure has more (ActiveSAS, ReadyToUpload,
// etc.) but the driver only models attached/unattached.
const (
	diskStateUnattached = "Unattached"
	diskStateAttached   = "Attached"
)

func diskStateFor(state string) string {
	if state == "in-use" {
		return diskStateAttached
	}

	return diskStateUnattached
}

func skuName(s *diskSKU) string {
	if s == nil {
		return ""
	}

	return s.Name
}

func skuTier(s *diskSKU) string {
	if s == nil {
		return ""
	}

	return s.Tier
}

// diskExtraSlots is the headroom for the cloudemu-internal tags mergeDiskTags
// inserts (name, resource group, createOption, source id).
const diskExtraSlots = 4

func mergeDiskTags(in map[string]string, name, resourceGroup, createOption, sourceID string) map[string]string {
	out := make(map[string]string, len(in)+diskExtraSlots)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = name

	if resourceGroup != "" {
		out[rgTag] = resourceGroup
	}

	if createOption != "" {
		out[createOptionTag] = createOption
	}

	if sourceID != "" {
		out[sourceIDTag] = sourceID
	}

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

func stripInternalDiskTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == armNameTag || k == rgTag || k == createOptionTag || k == sourceIDTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
