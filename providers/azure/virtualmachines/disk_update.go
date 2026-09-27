package virtualmachines

import (
	"net/url"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Managed disk SKUs (armcompute DiskStorageAccountTypes).
const (
	skuStandardLRS    = "Standard_LRS"
	skuStandardSSDLRS = "StandardSSD_LRS"
	skuStandardSSDZRS = "StandardSSD_ZRS"
	skuPremiumLRS     = "Premium_LRS"
	skuPremiumZRS     = "Premium_ZRS"
	skuPremiumV2LRS   = "PremiumV2_LRS"
	skuUltraSSDLRS    = "UltraSSD_LRS"
)

const (
	// osDiskDevice mirrors the Device marker server/azure/virtualmachines
	// attaches a VM's OS disk at, so the disk rules can tell an OS disk from a
	// data disk.
	osDiskDevice = "osdisk"

	// largeDiskBoundaryGiB is where Standard HDD/SSD and Premium SSD move to a
	// different storage back end. Crossing it needs the disk detached.
	largeDiskBoundaryGiB = 4096

	// maxDiskSizeGiB / maxProvisionedDiskSizeGiB are the largest managed disk
	// sizes (Standard/Premium SSD, and Ultra Disk / Premium SSD v2).
	maxDiskSizeGiB            = 32767
	maxProvisionedDiskSizeGiB = 65536

	// sasTimeLayout is the se= (signed expiry) format GrantDiskAccess writes.
	sasTimeLayout = "2006-01-02T15:04:05Z"

	// ARM error codes for a disk update Azure refuses because of the disk's
	// state rather than the request.
	codeOperationNotAllowed     = "OperationNotAllowed"
	codeResizeWhileActiveSAS    = "ChangeDiskSizeWhileActiveSasNotAllowed"
	codeInvalidResizeLargeDisks = "InvalidResizeForLargeDisks"
)

// knownDiskSKUs are the storage account types a disk can be converted to, by
// lower-cased name, mapped to their canonical spelling.
var knownDiskSKUs = map[string]string{ //nolint:gochecknoglobals // lookup table
	strings.ToLower(skuStandardLRS):    skuStandardLRS,
	strings.ToLower(skuStandardSSDLRS): skuStandardSSDLRS,
	strings.ToLower(skuStandardSSDZRS): skuStandardSSDZRS,
	strings.ToLower(skuPremiumLRS):     skuPremiumLRS,
	strings.ToLower(skuPremiumZRS):     skuPremiumZRS,
	strings.ToLower(skuPremiumV2LRS):   skuPremiumV2LRS,
	strings.ToLower(skuUltraSSDLRS):    skuUltraSSDLRS,
}

// premiumTiers are the Premium SSD performance tiers in ascending order, each
// with the largest disk size (GiB) it is the baseline for. From the
// "Performance tiers for Azure Premium SSD managed disks" table on Microsoft
// Learn (virtual-machines/disks-change-performance).
var premiumTiers = []struct { //nolint:gochecknoglobals // lookup table
	name    string
	maxSize int
}{
	{"P1", 4}, {"P2", 8}, {"P3", 16}, {"P4", 32}, {"P6", 64}, {"P10", 128},
	{"P15", 256}, {"P20", 512}, {"P30", 1024}, {"P40", 2048}, {"P50", 4096},
	{"P60", 8192}, {"P70", 16384}, {"P80", maxDiskSizeGiB},
}

// p50Index is the position of P50, the highest tier a disk of 4 TiB or less
// may use: P60, P70 and P80 need a disk larger than 4,096 GiB.
const p50Index = 10

// patchVolume merges patch over the stored volume id and validates the result
// against Azure's managed-disk update rules, all inside one store-lock span
// with copy-on-write (like AttachVolume), so a concurrent attach, detach or
// update is never overwritten with a stale copy. location, when non-empty,
// replaces the stored region (the PUT path).
func (m *Mock) patchVolume(id string, patch *driver.AzureDiskPatch, location string) (*driver.VolumeInfo, error) {
	var (
		out      driver.VolumeInfo
		patchErr error
	)

	ok := m.volumes.Update(id, func(cur *driver.VolumeInfo) *driver.VolumeInfo {
		next, err := m.mergeVolumePatch(cur, patch)
		if err != nil {
			patchErr = err
			return cur
		}

		if location != "" {
			next.Location = location
		}

		out = *next
		out.Tags = copyTags(next.Tags)

		return next
	})
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "disk %q not found", id)
	}

	if patchErr != nil {
		return nil, patchErr
	}

	return &out, nil
}

// volumeChange records which disk properties a patch actually changes.
type volumeChange struct {
	size, sku, tier, perf bool
}

func (c volumeChange) any() bool { return c.size || c.sku || c.tier || c.perf }

// mergeVolumePatch returns a new VolumeInfo with patch applied over cur, or
// the error real Azure answers the update with. It never mutates cur. Request
// errors (400) are checked before state conflicts (409).
func (m *Mock) mergeVolumePatch(cur *driver.VolumeInfo, patch *driver.AzureDiskPatch) (*driver.VolumeInfo, error) {
	next := *cur
	next.Tags = patchVolumeTags(cur.Tags, patch.Tags, patch.KeepTags)

	var chg volumeChange

	if err := applySizePatch(&next, cur, patch.Size, &chg); err != nil {
		return nil, err
	}

	if err := applySKUPatch(&next, cur, patch.VolumeType, &chg); err != nil {
		return nil, err
	}

	if err := applyPerfPatch(&next, cur, patch, &chg); err != nil {
		return nil, err
	}

	if err := applyTierPatch(&next, cur, patch.Tier, &chg); err != nil {
		return nil, err
	}

	if !chg.any() {
		return &next, nil
	}

	if chg.size {
		if err := checkMaxSize(&next); err != nil {
			return nil, err
		}
	}

	if err := m.checkVolumeState(cur, &next, chg); err != nil {
		return nil, err
	}

	return &next, nil
}

// applySizePatch grows the disk. Azure never shrinks a managed disk
// (InvalidParameter / InvalidResizeWithName) and rejects a size above the
// SKU's maximum.
func applySizePatch(next, cur *driver.VolumeInfo, size *int, chg *volumeChange) error {
	if size == nil || *size == cur.Size {
		return nil
	}

	if *size < cur.Size {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Disk size can only be increased. Current size is %d GB, requested size is %d GB.", cur.Size, *size)
	}

	next.Size = *size
	chg.size = true

	return nil
}

// applySKUPatch converts the disk to another storage account type, subject to
// checkSKUConversion. A conversion drops the provisioned IOPS/throughput of a
// SKU that does not carry them.
func applySKUPatch(next, cur *driver.VolumeInfo, sku *string, chg *volumeChange) error {
	if sku == nil || *sku == "" || strings.EqualFold(*sku, cur.VolumeType) {
		return nil
	}

	target, ok := knownDiskSKUs[strings.ToLower(*sku)]
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "The value '%s' of parameter 'sku.name' is not valid.", *sku)
	}

	if err := checkSKUConversion(cur, target); err != nil {
		return err
	}

	next.VolumeType = target
	chg.sku = true

	if !provisionedPerfSKU(target) {
		next.IOPS = 0
		next.Throughput = 0
	}

	return nil
}

// checkSKUConversion applies Microsoft Learn (virtual-machines/
// disks-convert-types): an Ultra Disk can be neither the source nor the target
// of a conversion, a Premium SSD v2 cannot be converted to anything else, and
// an OS disk cannot become a Premium SSD v2.
func checkSKUConversion(cur *driver.VolumeInfo, target string) error {
	switch {
	case strings.EqualFold(cur.VolumeType, skuUltraSSDLRS) || target == skuUltraSSDLRS:
		return cerrors.Newf(cerrors.InvalidArgument,
			"Changing the SKU of disk %q from %s to %s is not supported. Create a new disk from a snapshot instead.",
			cur.ID, cur.VolumeType, target)
	case strings.EqualFold(cur.VolumeType, skuPremiumV2LRS):
		return cerrors.Newf(cerrors.InvalidArgument,
			"A %s disk cannot be converted to another disk type. Migrate it using a snapshot instead.", skuPremiumV2LRS)
	case target == skuPremiumV2LRS && cur.Device == osDiskDevice:
		return cerrors.Newf(cerrors.InvalidArgument, "An OS disk cannot be converted to %s.", skuPremiumV2LRS)
	}

	return nil
}

// applyPerfPatch sets diskIOPSReadWrite / diskMBpsReadWrite, which Azure only
// accepts on UltraSSD_LRS and PremiumV2_LRS. Clearing them is always allowed
// (a re-PUT that omits them).
func applyPerfPatch(next, cur *driver.VolumeInfo, patch *driver.AzureDiskPatch, chg *volumeChange) error {
	iops := changedInt(patch.IOPS, cur.IOPS)
	mbps := changedInt(patch.Throughput, cur.Throughput)

	if iops == nil && mbps == nil {
		return nil
	}

	provisioned := provisionedPerfSKU(next.VolumeType)

	if !provisioned && (nonZero(iops) || nonZero(mbps)) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Property 'diskIOPSReadWrite'/'diskMBpsReadWrite' can only be set on %s and %s disks; disk SKU is %q.",
			skuUltraSSDLRS, skuPremiumV2LRS, next.VolumeType)
	}

	setInt(&next.IOPS, iops)
	setInt(&next.Throughput, mbps)

	chg.perf = provisioned

	return nil
}

// changedInt returns v when it is set and differs from cur, else nil.
func changedInt(v *int, cur int) *int {
	if v == nil || *v == cur {
		return nil
	}

	return v
}

func nonZero(v *int) bool { return v != nil && *v != 0 }

func setInt(dst, v *int) {
	if v != nil {
		*dst = *v
	}
}

// applyTierPatch sets the performance tier and keeps it consistent with the
// SKU and size. Per Microsoft Learn (virtual-machines/disks-change-performance)
// a tier is only supported on Premium SSD, cannot be below the size's
// baseline tier, and P60-P80 need a disk larger than 4,096 GiB. Moving to
// Premium SSD sets the baseline tier, moving off it clears the tier, and
// growing a Premium SSD past its tier raises the tier to the new baseline.
func applyTierPatch(next, cur *driver.VolumeInfo, tier *string, chg *volumeChange) error {
	if tier != nil && *tier != "" && !strings.EqualFold(*tier, cur.Tier) {
		return applyExplicitTier(next, *tier, chg)
	}

	premium := premiumSSD(next.VolumeType)
	baseline := premiumTiers[baselineTierIndex(next.Size)].name

	switch {
	case chg.sku && premium:
		next.Tier = baseline
	case chg.sku:
		next.Tier = ""
	case chg.size && premium && tierIndex(next.Tier) < baselineTierIndex(next.Size):
		next.Tier = baseline
	}

	return nil
}

// applyExplicitTier sets a requested performance tier.
func applyExplicitTier(next *driver.VolumeInfo, tier string, chg *volumeChange) error {
	if !premiumSSD(next.VolumeType) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Performance tier '%s' can only be set on %s and %s disks; disk SKU is %q.",
			tier, skuPremiumLRS, skuPremiumZRS, next.VolumeType)
	}

	idx := tierIndex(tier)
	if err := validTierFor(tier, idx, next.Size); err != nil {
		return err
	}

	next.Tier = premiumTiers[idx].name
	chg.tier = true

	return nil
}

// validTierFor checks that the tier at idx is a valid performance tier for a
// Premium SSD of size GiB.
func validTierFor(tier string, idx, size int) error {
	if idx < 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "The value '%s' of parameter 'tier' is not valid.", tier)
	}

	base := baselineTierIndex(size)
	if idx < base {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Performance tier '%s' is below the baseline tier '%s' of a %d GiB disk.", tier, premiumTiers[base].name, size)
	}

	if idx > p50Index && size <= largeDiskBoundaryGiB {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Performance tier '%s' is only available on disks larger than %d GiB.", tier, largeDiskBoundaryGiB)
	}

	return nil
}

// checkVolumeState refuses a property change the disk's current state does not
// allow (Microsoft Learn: troubleshoot/azure/virtual-machines/windows/
// troubleshoot-disk-resize and virtual-machines/windows/expand-disks). An
// active SAS (beginGetAccess) holds a lease and blocks every change; the
// attachment rules are in checkAttachedChange.
func (m *Mock) checkVolumeState(cur, next *driver.VolumeInfo, chg volumeChange) error {
	if m.activeDiskSAS(cur.ID) {
		code := codeOperationNotAllowed
		if chg.size {
			code = codeResizeWhileActiveSAS
		}

		return diskConflict(code, "Cannot change disk %q while it has an active SAS URI. Revoke access to the disk "+
			"(endGetAccess) and retry the operation.", cur.ID)
	}

	if cur.State != stateInUse || cur.AttachedTo == "" {
		return nil
	}

	return checkAttachedChange(cur, next, chg, m.vmAllocated(cur.AttachedTo))
}

// checkAttachedChange applies the rules for an attached disk: an OS disk is
// only resized, and any disk only converted to another SKU, while its VM is
// deallocated (data disks can be expanded online), and a Standard HDD/SSD or
// Premium SSD of 4 TiB or less cannot grow past 4 TiB while attached.
func checkAttachedChange(cur, next *driver.VolumeInfo, chg volumeChange, allocated bool) error {
	if chg.sku && allocated {
		return diskConflict(codeOperationNotAllowed, "Cannot change the SKU of disk %q while it is attached to running "+
			"VM %q. Changing the disk type requires the virtual machine to be deallocated.", cur.ID, cur.AttachedTo)
	}

	if !chg.size {
		return nil
	}

	if cur.Device == osDiskDevice && allocated {
		return diskConflict(codeOperationNotAllowed, "Cannot resize disk %q while it is attached to running VM %q. "+
			"Resizing a disk of an Azure Virtual Machine requires the virtual machine to be deallocated. "+
			"Please stop your VM and retry the operation.", cur.ID, cur.AttachedTo)
	}

	if !provisionedPerfSKU(cur.VolumeType) && cur.Size <= largeDiskBoundaryGiB && next.Size > largeDiskBoundaryGiB {
		return diskConflict(codeInvalidResizeLargeDisks, "Disk %q of %d GiB cannot be expanded beyond %d GiB while "+
			"it is attached. Detach the disk and retry the operation.", cur.ID, cur.Size, largeDiskBoundaryGiB)
	}

	return nil
}

// checkMaxSize rejects a size above the largest disk the SKU offers.
func checkMaxSize(vol *driver.VolumeInfo) error {
	limit := maxDiskSizeGiB
	if provisionedPerfSKU(vol.VolumeType) {
		limit = maxProvisionedDiskSizeGiB
	}

	if vol.Size > limit {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Disk size %d GB is larger than the maximum %d GB for a %s disk.", vol.Size, limit, vol.VolumeType)
	}

	return nil
}

// diskConflict builds the 409 an ARM disk update is refused with.
func diskConflict(code, format string, args ...any) error {
	return &driver.AzureDiskError{Code: code, Err: cerrors.Newf(cerrors.FailedPrecondition, format, args...)}
}

// vmAllocated reports whether the VM holds compute (any power state but
// deallocated). A VM stopped from inside the guest is still allocated, and
// Azure still refuses the change. An unknown VM counts as not allocated.
func (m *Mock) vmAllocated(instanceID string) bool {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return false
	}

	return inst.PowerState != powerStateDeallocated
}

// activeDiskSAS reports whether a SAS granted by GrantDiskAccess is still
// live: not revoked and not past its se= expiry on the mock's clock.
func (m *Mock) activeDiskSAS(volumeID string) bool {
	sas, ok := m.diskAccess.Get(volumeID)
	if !ok {
		return false
	}

	u, err := url.Parse(sas)
	if err != nil {
		return true
	}

	expiry, err := time.Parse(sasTimeLayout, u.Query().Get("se"))
	if err != nil {
		return true
	}

	return m.opts.Clock.Now().Before(expiry)
}

// patchVolumeTags returns the tag set to store: a copy of existing when the
// patch omits tags, otherwise the patch tags plus the existing keys in keep.
func patchVolumeTags(existing, patch map[string]string, keep []string) map[string]string {
	if patch == nil {
		return copyTags(existing)
	}

	out := make(map[string]string, len(patch)+len(keep))
	for k, v := range patch {
		out[k] = v
	}

	for _, k := range keep {
		if v, ok := existing[k]; ok {
			out[k] = v
		}
	}

	return out
}

// provisionedPerfSKU reports whether a disk SKU lets the caller set IOPS and
// throughput independently of size.
func provisionedPerfSKU(sku string) bool {
	return strings.EqualFold(sku, skuUltraSSDLRS) || strings.EqualFold(sku, skuPremiumV2LRS)
}

// premiumSSD reports whether a disk SKU is Premium SSD, the only type with
// performance tiers.
func premiumSSD(sku string) bool {
	return strings.EqualFold(sku, skuPremiumLRS) || strings.EqualFold(sku, skuPremiumZRS)
}

// tierIndex returns the position of a performance tier name in premiumTiers,
// or -1 for an unknown name (including "").
func tierIndex(tier string) int {
	for i := range premiumTiers {
		if strings.EqualFold(premiumTiers[i].name, tier) {
			return i
		}
	}

	return -1
}

// baselineTierIndex returns the position of the baseline tier Azure selects
// for a Premium SSD of size GiB.
func baselineTierIndex(size int) int {
	for i := range premiumTiers {
		if size <= premiumTiers[i].maxSize {
			return i
		}
	}

	return len(premiumTiers) - 1
}
