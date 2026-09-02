// Package images serves Azure ARM Microsoft.Compute/images requests.
package images

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "images"
	armNameTag      = "cloudemu:azureImageName"
	rgTag           = "cloudemu:azureRG"
	sourceVMTag     = "cloudemu:sourceVM"
	osTypeTag       = "cloudemu:osType"
	defaultLocation = "eastus"

	// vmARMNameTag mirrors the constant in server/azure/virtualmachines so
	// we can resolve a VM ARM ID to its driver-internal instance ID.
	vmARMNameTag = "cloudemu:azureName"

	// diskARMNameTag mirrors the constant in server/azure/disks so we can
	// resolve a source-disk ARM ID to its stored volume when creating a
	// managed image directly from a disk.
	diskARMNameTag = "cloudemu:azureDiskName"

	// defaultOSDiskSizeGB is the OS-disk size we report for a captured image
	// when the source disk size is unknown (Azure's marketplace default).
	defaultOSDiskSizeGB = 30
)

// Handler serves Microsoft.Compute/images requests.
type Handler struct {
	compute computedriver.Compute
}

// New returns an images handler.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

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
	case http.MethodPatch:
		h.updateTags(w, r, rp)
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

	imgs, err := h.compute.DescribeImages(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]imageResponse, 0, len(imgs))

	for i := range imgs {
		if rp.ResourceGroup != "" && !strings.EqualFold(tagOr(imgs[i].Tags, rgTag, ""), rp.ResourceGroup) {
			continue
		}

		name := tagOr(imgs[i].Tags, armNameTag, imgs[i].Name)
		scope := rp
		scope.ResourceName = name
		out = append(out, toImageResponse(&imgs[i], scope, ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, imageListResponse{Value: out})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req imageRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cfg, err := h.buildImageConfig(r.Context(), rp, &req)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM CreateOrUpdate is idempotent: replace an existing image in place
	// rather than accumulating a duplicate under the same name.
	if existing, findErr := findImageByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); findErr == nil {
		_ = h.compute.DeregisterImage(r.Context(), existing.ID)
	}

	img, err := h.compute.CreateImage(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	body := toImageResponse(img, rp, location)

	// Azure SDK images poller is finicky about LRO terminal-state detection.
	// Return 200 OK directly (sync semantics) so PollUntilDone resolves on
	// the response body's ProvisioningState alone, no header polling.
	azurearm.WriteJSON(w, http.StatusOK, body)
}

// updateTags applies an ARM PATCH (Images - Update / ImageUpdate): the tags in
// the body are merged into the image's existing tags, every other field is
// preserved, and the full image resource is returned in GET shape. A PATCH on a
// missing image is a 404. cloudemu-internal tags are always preserved because
// the merge starts from the stored tag set.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) updateTags(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	updater, ok := h.compute.(computedriver.ImageTagUpdater)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "image tag update is not supported")
		return
	}

	var req imageRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	// Merge the user tags, then re-assert the cloudemu-internal bookkeeping tags
	// from the stored image exactly as PUT does. A PATCH must never let a caller
	// rewrite the reserved cloudemu: namespace (image name, resource group,
	// source VM, OS type) — those keys carry the image's ARM identity, so they
	// stay immutable across the update.
	name := tagOr(img.Tags, armNameTag, img.Name)
	merged := mergeTags(
		mergeUserTags(img.Tags, withoutReservedTags(req.Tags)),
		name, tagOr(img.Tags, rgTag, ""), tagOr(img.Tags, sourceVMTag, ""), tagOr(img.Tags, osTypeTag, ""),
	)

	updated, err := updater.UpdateImageTags(r.Context(), img.ID, merged)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toImageResponse(updated, rp, req.Location))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toImageResponse(img, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	img, err := findImageByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeregisterImage(r.Context(), img.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeImageAsync(w, r, rp.Subscription, "img-delete-"+rp.ResourceName, nil)
}

func findImageByName(ctx context.Context, c computedriver.Compute, resourceGroup, name string) (*computedriver.ImageInfo, error) {
	imgs, err := c.DescribeImages(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range imgs {
		if tagOr(imgs[i].Tags, armNameTag, imgs[i].Name) != name {
			continue
		}

		if resourceGroup != "" && tagOr(imgs[i].Tags, rgTag, "") != resourceGroup {
			continue
		}

		return &imgs[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "image %s not found", name)
}

// resolveSourceVMID translates an ARM VM resource ID to the driver-internal
// instance ID by looking up the VM via its ARM-name tag.
func (h *Handler) resolveSourceVMID(ctx context.Context, src string) (string, error) {
	if src == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "sourceVirtualMachine.id is required")
	}

	idx := strings.LastIndex(src, "/virtualMachines/")
	if idx < 0 {
		return src, nil
	}

	name := src[idx+len("/virtualMachines/"):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}

	insts, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return "", err
	}

	for i := range insts {
		if tagOr(insts[i].Tags, vmARMNameTag, "") == name {
			return insts[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "source VM %s not found", name)
}

// buildImageConfig maps an ARM image request to a driver ImageConfig, choosing
// the VM-capture path when sourceVirtualMachine is set and the disk-source path
// when only storageProfile.osDisk.managedDisk.id is present.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) buildImageConfig(ctx context.Context, rp azurearm.ResourcePath, req *imageRequest) (computedriver.ImageConfig, error) {
	submittedVM := sourceVMID(req.Properties.SourceVirtualMachine)
	if submittedVM != "" {
		driverInstanceID, err := h.resolveSourceVMID(ctx, submittedVM)
		if err != nil {
			return computedriver.ImageConfig{}, err
		}

		return computedriver.ImageConfig{
			InstanceID: driverInstanceID,
			Name:       rp.ResourceName,
			Tags: mergeTags(
				req.Tags, rp.ResourceName, rp.ResourceGroup,
				submittedVM, h.sourceOSType(ctx, driverInstanceID),
			),
		}, nil
	}

	return h.diskImageConfig(ctx, rp, req)
}

// diskImageConfig builds the config for a managed image created directly from a
// source OS disk (no VM capture). It validates the disk exists and captures its
// size onto the image.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) diskImageConfig(ctx context.Context, rp azurearm.ResourcePath, req *imageRequest) (computedriver.ImageConfig, error) {
	osDisk := osDiskOf(req.Properties.StorageProfile)

	diskID := managedDiskID(osDisk)
	if diskID == "" {
		return computedriver.ImageConfig{}, cerrors.New(cerrors.InvalidArgument,
			"sourceVirtualMachine.id or storageProfile.osDisk.managedDisk.id is required")
	}

	vol, err := h.resolveSourceDisk(ctx, diskID, rp.ResourceGroup)
	if err != nil {
		return computedriver.ImageConfig{}, err
	}

	return computedriver.ImageConfig{
		Name:       rp.ResourceName,
		OSDiskID:   diskID,
		OSType:     osTypeOr(osDisk.OSType),
		OSState:    osStateOr(osDisk.OSState),
		DiskSizeGB: diskSizeOr(vol.Size, osDisk.DiskSizeGB),
		Tags:       mergeTags(req.Tags, rp.ResourceName, rp.ResourceGroup, "", ""),
	}, nil
}

// resolveSourceDisk finds the stored volume backing a source-disk ARM ID by its
// ARM-tagged name, mirroring how disks/handler.go resolves a disk by name.
func (h *Handler) resolveSourceDisk(ctx context.Context, diskID, resourceGroup string) (*computedriver.VolumeInfo, error) {
	name := armSegment(diskID, "/disks/")
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "storageProfile.osDisk.managedDisk.id is malformed")
	}

	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range vols {
		if tagOr(vols[i].Tags, diskARMNameTag, "") != name {
			continue
		}

		if resourceGroup != "" && tagOr(vols[i].Tags, rgTag, "") != resourceGroup {
			continue
		}

		return &vols[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "source disk %s not found", name)
}

func sourceVMID(r *resourceRef) string {
	if r == nil {
		return ""
	}

	return r.ID
}

func osDiskOf(sp *imageRequestStorageProfile) *imageRequestOSDisk {
	if sp == nil {
		return nil
	}

	return sp.OSDisk
}

func managedDiskID(d *imageRequestOSDisk) string {
	if d == nil || d.ManagedDisk == nil {
		return ""
	}

	return d.ManagedDisk.ID
}

// armSegment extracts the resource name that follows segment in an ARM ID
// (e.g. the disk name after "/disks/").
func armSegment(id, segment string) string {
	idx := strings.LastIndex(id, segment)
	if idx < 0 {
		return ""
	}

	name := id[idx+len(segment):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}

	return name
}

// osStateOr defaults an unspecified image OS state to Generalized, matching the
// VM-capture default.
func osStateOr(osState string) string {
	if osState == "" {
		return "Generalized"
	}

	return osState
}

// diskSizeOr prefers the actual source-disk size, falling back to a
// client-supplied size and finally the marketplace default.
func diskSizeOr(actual, requested int) int {
	switch {
	case actual > 0:
		return actual
	case requested > 0:
		return requested
	default:
		return defaultOSDiskSizeGB
	}
}

// sourceOSType returns the guest OS family of the source VM (driver instance
// id), so the captured image echoes the OS type Azure records on osDisk.
func (h *Handler) sourceOSType(ctx context.Context, instanceID string) string {
	insts, err := h.compute.DescribeInstances(ctx, []string{instanceID}, nil)
	if err != nil || len(insts) == 0 {
		return ""
	}

	return insts[0].OSType
}

func writeImageAsync(w http.ResponseWriter, r *http.Request, sub, opID string, body any) {
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
func toImageResponse(img *computedriver.ImageInfo, rp azurearm.ResourcePath, location string) imageResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(img.Tags, armNameTag, img.Name)

	props := imageResponseProps{
		ProvisioningState: "Succeeded",
		StorageProfile:    &imageStorageProfile{OSDisk: osDiskResponse(img)},
	}

	// A disk-sourced image has no sourceVirtualMachine; a VM-captured one echoes
	// the captured VM reference exactly as before (byte-unchanged).
	if img.OSDiskID == "" {
		if src := tagOr(img.Tags, sourceVMTag, ""); src != "" {
			props.SourceVirtualMachine = &resourceRef{ID: src}
		}
	}

	return imageResponse{
		ID:         azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:       name,
		Type:       providerName + "/" + resourceType,
		Location:   location,
		Tags:       stripInternalTags(img.Tags),
		Properties: props,
	}
}

// osDiskResponse builds the image's osDisk echo. A disk-sourced image reports
// its captured managedDisk and disk size; a VM-captured image keeps its prior
// tag-derived shape byte-for-byte.
func osDiskResponse(img *computedriver.ImageInfo) *imageOSDisk {
	if img.OSDiskID != "" {
		return &imageOSDisk{
			OSType:             osTypeOr(img.OSType),
			OSState:            osStateOr(img.OSState),
			ManagedDisk:        &resourceRef{ID: img.OSDiskID},
			DiskSizeGB:         img.DiskSizeGB,
			StorageAccountType: "Standard_LRS",
		}
	}

	return &imageOSDisk{
		OSType:             osTypeOr(tagOr(img.Tags, osTypeTag, "")),
		OSState:            "Generalized",
		DiskSizeGB:         defaultOSDiskSizeGB,
		StorageAccountType: "Standard_LRS",
	}
}

// osTypeOr defaults an unknown captured-image OS type to Linux, the most common
// case, so the osDisk always reports a concrete osType.
func osTypeOr(osType string) string {
	if osType == "" {
		return "Linux"
	}

	return osType
}

// imgExtraSlots is the headroom for the cloudemu-internal tags mergeTags
// inserts (name, resource group, source VM, OS type).
const imgExtraSlots = 4

func mergeTags(in map[string]string, name, resourceGroup, sourceVM, osType string) map[string]string {
	out := make(map[string]string, len(in)+imgExtraSlots)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = name

	if resourceGroup != "" {
		out[rgTag] = resourceGroup
	}

	if sourceVM != "" {
		out[sourceVMTag] = sourceVM
	}

	if osType != "" {
		out[osTypeTag] = osType
	}

	return out
}

// reservedTagPrefix is the namespace of the cloudemu-internal bookkeeping tags
// (armNameTag, rgTag, sourceVMTag, osTypeTag). A PATCH caller may not set any tag
// in it — those keys carry the image's ARM identity.
const reservedTagPrefix = "cloudemu:"

// withoutReservedTags drops any cloudemu:-prefixed key from a PATCH-supplied tag
// map so a caller cannot rewrite the image's internal bookkeeping tags.
func withoutReservedTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, reservedTagPrefix) {
			continue
		}

		out[k] = v
	}

	return out
}

// mergeUserTags overlays the PATCH-supplied tags onto the image's existing tag
// set (which still carries the cloudemu-internal tags), so untouched tags — and
// every internal tag — survive the update. A nil incoming map is a no-op merge.
func mergeUserTags(existing, incoming map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(incoming))

	for k, v := range existing {
		out[k] = v
	}

	for k, v := range incoming {
		out[k] = v
	}

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
		if k == armNameTag || k == rgTag || k == sourceVMTag || k == osTypeTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
