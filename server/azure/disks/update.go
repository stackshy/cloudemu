package disks

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Disk SKUs whose provisioned performance (diskIOPSReadWrite /
// diskMBpsReadWrite) is caller-settable. Every other SKU derives IOPS and
// throughput from its size/tier, and Azure rejects an explicit value.
const (
	skuUltraSSD    = "UltraSSD_LRS"
	skuPremiumV2   = "PremiumV2_LRS"
	errCodeBadReq  = "BadRequest"
	errCodeInvalid = "InvalidParameter"
)

// diskPatchError is a validation failure of a disk PATCH body, rendered as an
// ARM 400 with the given code.
type diskPatchError struct {
	code, msg string
}

// update handles PATCH .../disks/{name} (armcompute DisksClient.BeginUpdate).
// The body is a DiskUpdate: every field is optional and only the supplied ones
// change. Like CreateOrUpdate it answers 202 + Azure-AsyncOperation, and the
// SDK poller's final GET reads the updated disk.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
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

	cfg, perr := applyDiskPatch(existing, &req)
	if perr != nil {
		azurearm.WriteError(w, http.StatusBadRequest, perr.code, perr.msg)
		return
	}

	vol, err := h.updateExistingDisk(r.Context(), existing, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDiskAsync(w, r, rp.Subscription, "disk-update-"+rp.ResourceName,
		h.toDiskResponse(r.Context(), vol, rp, ""))
}

// applyDiskPatch folds a DiskUpdate body over the existing volume and returns
// the full VolumeConfig to store. Omitted fields keep their current value.
// Tags, when present, replace the user tag set wholesale (ARM Compute PATCH
// semantics, as for virtualMachines); the cloudemu-internal bookkeeping tags
// are always preserved.
func applyDiskPatch(vol *computedriver.VolumeInfo, req *diskUpdateRequest) (computedriver.VolumeConfig, *diskPatchError) {
	cfg := computedriver.VolumeConfig{
		Size:             vol.Size,
		VolumeType:       vol.VolumeType,
		IOPS:             vol.IOPS,
		Throughput:       vol.Throughput,
		Tier:             vol.Tier,
		Location:         vol.Location,
		AvailabilityZone: vol.AvailabilityZone,
		Tags:             patchDiskTags(vol.Tags, req.Tags),
	}

	if req.SKU != nil {
		applySKUPatch(&cfg, req.SKU)
	}

	if req.Properties == nil {
		return cfg, nil
	}

	perr := applyPropsPatch(&cfg, vol.Size, req.Properties)

	return cfg, perr
}

// applySKUPatch switches the disk's storage account type (and tier, when
// given). Moving to a SKU without caller-settable performance drops any
// provisioned IOPS/throughput, which only UltraSSD_LRS/PremiumV2_LRS carry.
func applySKUPatch(cfg *computedriver.VolumeConfig, sku *diskSKU) {
	if sku.Name != "" {
		cfg.VolumeType = sku.Name
	}

	if sku.Tier != "" {
		cfg.Tier = sku.Tier
	}

	if !provisionedPerfSKU(cfg.VolumeType) {
		cfg.IOPS = 0
		cfg.Throughput = 0
	}
}

// applyPropsPatch applies diskSizeGB (grow only), tier and the provisioned
// performance fields. Azure never shrinks a managed disk, and only accepts
// diskIOPSReadWrite/diskMBpsReadWrite on UltraSSD_LRS and PremiumV2_LRS.
func applyPropsPatch(cfg *computedriver.VolumeConfig, currentSize int, p *diskUpdateRequestProps) *diskPatchError {
	if p.DiskSizeGB != nil {
		if *p.DiskSizeGB < currentSize {
			return &diskPatchError{code: errCodeBadReq, msg: fmt.Sprintf(
				"Disk size can only be increased. Current size is %d GB, requested size is %d GB.",
				currentSize, *p.DiskSizeGB)}
		}

		cfg.Size = *p.DiskSizeGB
	}

	if p.Tier != nil && *p.Tier != "" {
		cfg.Tier = *p.Tier
	}

	if p.DiskIOPSReadWrite == nil && p.DiskMBpsReadWrite == nil {
		return nil
	}

	if !provisionedPerfSKU(cfg.VolumeType) {
		return &diskPatchError{code: errCodeInvalid, msg: fmt.Sprintf(
			"Property 'diskIOPSReadWrite'/'diskMBpsReadWrite' can only be set on %s and %s disks; disk SKU is %q.",
			skuUltraSSD, skuPremiumV2, cfg.VolumeType)}
	}

	if p.DiskIOPSReadWrite != nil {
		cfg.IOPS = *p.DiskIOPSReadWrite
	}

	if p.DiskMBpsReadWrite != nil {
		cfg.Throughput = *p.DiskMBpsReadWrite
	}

	return nil
}

// provisionedPerfSKU reports whether a disk SKU lets the caller set IOPS and
// throughput independently of size.
func provisionedPerfSKU(sku string) bool {
	return strings.EqualFold(sku, skuUltraSSD) || strings.EqualFold(sku, skuPremiumV2)
}

// patchDiskTags returns the tag set to store: the existing tags when the PATCH
// omitted tags, otherwise the supplied user tags plus the existing internal
// bookkeeping tags.
func patchDiskTags(existing, patch map[string]string) map[string]string {
	if patch == nil {
		out := make(map[string]string, len(existing))
		for k, v := range existing {
			out[k] = v
		}

		return out
	}

	out := make(map[string]string, len(patch)+diskExtraSlots)
	for k, v := range patch {
		out[k] = v
	}

	for _, k := range []string{armNameTag, rgTag, createOptionTag, sourceIDTag} {
		if v, ok := existing[k]; ok {
			out[k] = v
		}
	}

	return out
}
