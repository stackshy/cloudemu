package disks

import (
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// internalDiskTags are the cloudemu bookkeeping tags a disk carries. A PATCH
// that replaces the user tags keeps them.
var internalDiskTags = []string{armNameTag, rgTag, createOptionTag, sourceIDTag} //nolint:gochecknoglobals // fixed list

// update handles PATCH .../disks/{name} (armcompute DisksClient.BeginUpdate).
// The body is a DiskUpdate: every field is optional and only the supplied ones
// change. Like CreateOrUpdate it answers 202 + Azure-AsyncOperation, and the
// SDK poller's final GET reads the updated disk. The merge and Azure's rules
// (grow-only, SKU conversion, performance tier, attached disk, active SAS) run
// in the provider, so the Go library and serve behave the same.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	patcher, ok := h.compute.(computedriver.AzureDiskPatcher)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "disk update not supported")
		return
	}

	existing, err := findDiskByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req diskUpdateRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	vol, err := patcher.PatchVolume(r.Context(), existing.ID, toDiskPatch(&req))
	if err != nil {
		writeDiskErr(w, err)
		return
	}

	writeDiskAsync(w, r, rp.Subscription, "disk-update-"+rp.ResourceName,
		h.toDiskResponse(r.Context(), vol, rp, ""))
}

// toDiskPatch maps a DiskUpdate body onto the provider's partial update.
// sku.tier is read-only in the ARM contract (it follows sku.name) and is
// ignored; the performance tier is properties.tier.
func toDiskPatch(req *diskUpdateRequest) computedriver.AzureDiskPatch {
	patch := computedriver.AzureDiskPatch{Tags: req.Tags, KeepTags: internalDiskTags}

	if req.SKU != nil && req.SKU.Name != "" {
		name := req.SKU.Name
		patch.VolumeType = &name
	}

	if p := req.Properties; p != nil {
		patch.Size = p.DiskSizeGB
		patch.Tier = p.Tier
		patch.IOPS = p.DiskIOPSReadWrite
		patch.Throughput = p.DiskMBpsReadWrite
	}

	return patch
}

// writeDiskErr writes a disk update error. A refusal the provider tagged with
// an ARM error code (OperationNotAllowed, ChangeDiskSizeWhileActiveSasNotAllowed,
// ...) is echoed with that code; anything else maps as usual.
func writeDiskErr(w http.ResponseWriter, err error) {
	var de *computedriver.AzureDiskError
	if !errors.As(err, &de) {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusConflict
	if cerrors.IsInvalidArgument(err) {
		status = http.StatusBadRequest
	}

	azurearm.WriteError(w, status, de.Code, cerrors.Message(err))
}
