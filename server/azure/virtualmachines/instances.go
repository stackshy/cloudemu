package virtualmachines

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// armNameTag is the tag key we use to round-trip the ARM resource name
// through the driver, since the driver indexes by its own ID.
const armNameTag = "cloudemu:azureName"

// diskARMNameTag and diskRGTag mirror the constants in server/azure/disks so
// a storageProfile.dataDisks managedDisk.id can be resolved to its driver
// volume, and an attached volume's driver Device (the LUN we pass to
// AttachVolume) can be echoed back on GET/LIST/CreateOrUpdate/Update.
const (
	diskARMNameTag      = "cloudemu:azureDiskName"
	diskRGTag           = "cloudemu:azureRG"
	diskCreateOptionTag = "cloudemu:createOption"
)

// osDiskDevice is the driver Device marker a materialized OS disk is attached
// at, distinguishing it from data disks (which attach at a numeric LUN). It is
// non-numeric so parseLUN excludes the OS disk from data-disk reconciliation and
// from the storageProfile.dataDisks response.
const osDiskDevice = "osdisk"

// createOptionAttach references an existing managed disk rather than
// provisioning a new one.
const createOptionAttach = "Attach"

// deleteOptionDelete is the ARM deleteOption that cascades a disk's deletion
// with its VM (the alternative, "Detach", is the default and leaves the disk).
const deleteOptionDelete = "Delete"

// URL schemes for building absolute self-referential URLs (async operation
// status, boot-diagnostics serial log) against the incoming request.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// requestScheme reports the scheme the request arrived on, so self-referential
// URLs work on both the plain-HTTP and TLS listeners.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return schemeHTTPS
	}

	return schemeHTTP
}

// Driver lifecycle states we map to ARM PowerState codes.
const (
	stateRunning    = "running"
	statePending    = "pending"
	stateStopped    = "stopped"
	stateStopping   = "stopping"
	stateTerminated = "terminated"
)

// ARM PowerState codes (the suffix after "PowerState/") a VM may report.
const (
	powerCodeStopped     = "stopped"
	powerCodeDeallocated = "deallocated"
)

// createOrUpdate handles PUT virtualMachines/{name}. Maps the ARM JSON body
// onto an InstanceConfig, calls RunInstances(count=1), and replies with an
// ARM-shaped vmResponse.
//
//nolint:gocritic // rp is a request-scoped value; pointer chain isn't worth the noise
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req vmRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	// A networkProfile must reference NICs that already exist. Real Azure
	// rejects a VM whose networkProfile points at a missing NIC rather than
	// silently succeeding.
	nicRefs, err := h.validateNICs(r.Context(), req.Properties.NetworkProfile)
	if err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "NetworkInterfaceNotFound", err.Error())
		return
	}

	cfg := computedriver.InstanceConfig{
		ImageID:           imageRefToID(req.Properties.StorageProfile),
		InstanceType:      hardwareSize(req.Properties.HardwareProfile),
		SubnetID:          firstNicID(req.Properties.NetworkProfile),
		KeyName:           computerName(req.Properties.OSProfile),
		UserData:          decodeCustomData(customData(req.Properties.OSProfile)),
		Tags:              mergeTags(req.Tags, rp.ResourceName),
		Priority:          req.Properties.Priority,
		LicenseType:       req.Properties.LicenseType,
		OSType:            osTypeFromStorage(req.Properties.StorageProfile),
		Zones:             req.Zones,
		Region:            req.Location,
		ResourceGroup:     rp.ResourceGroup,
		Identity:          toDriverIdentity(req.Identity),
		NetworkInterfaces: nicRefs,
	}

	// ARM CreateOrUpdate is idempotent: a repeated PUT to the same {rg,name}
	// updates the VM in place rather than provisioning a duplicate.
	if existing, findErr := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); findErr == nil {
		h.updateExisting(w, r, rp, req, existing, cfg)
		return
	}

	instances, err := h.compute.RunInstances(r.Context(), cfg, 1)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if len(instances) == 0 {
		azurearm.WriteError(w, http.StatusInternalServerError, "InternalError", "driver returned zero instances")
		return
	}

	// Materialize the VM's OS managed disk as a real Microsoft.Compute/disks
	// resource so the disks API returns it, attached to the VM.
	if err := h.materializeOSDisk(r.Context(), rp, instances[0].ID, req.Properties.StorageProfile); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// storageProfile.dataDisks is the VM's complete desired data-disk state on
	// PUT (real Azure's full-replace semantics): materialize+attach every
	// implicit disk and attach every referenced managed disk not yet attached.
	if err := h.applyDataDisks(r.Context(), rp, instances[0].ID, dataDisksOf(req.Properties.StorageProfile), true); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// A create is a long-running operation: the initial response reports
	// "Creating" (201 Created) and settles to "Succeeded" on the next poll/GET.
	azurearm.WriteJSON(w, http.StatusCreated, h.buildVMResponse(r.Context(), &instances[0], rp, req, provisioningCreating))
}

// validateNICs verifies that every NIC referenced by a networkProfile exists,
// and resolves each reference down to the (resourceGroup, name) pair the
// driver layer needs to attach the NIC to the VM being created (setting its
// properties.virtualMachine back-reference). It returns (nil, nil) when no
// networking driver is wired or the driver does not implement the Azure
// network-interface surface, so handlers configured without networking keep
// their previous permissive behavior.
func (h *Handler) validateNICs(ctx context.Context, np *networkProfile) ([]computedriver.AzureNICRef, error) {
	if np == nil || len(np.NetworkInterfaces) == 0 || h.net == nil {
		return nil, nil
	}

	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return nil, nil
	}

	refs := make([]computedriver.AzureNICRef, 0, len(np.NetworkInterfaces))

	for _, nic := range np.NetworkInterfaces {
		if nic.ID == "" {
			continue
		}

		pp, ok := azurearm.ParsePath(nic.ID)
		if !ok || pp.ResourceName == "" {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "malformed networkInterface id %q", nic.ID)
		}

		if _, err := svc.GetNetworkInterface(ctx, pp.ResourceGroup, pp.ResourceName); err != nil {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"networkProfile references network interface %q which does not exist", pp.ResourceName)
		}

		refs = append(refs, computedriver.AzureNICRef{ResourceGroup: pp.ResourceGroup, Name: pp.ResourceName})
	}

	return refs, nil
}

// updateExisting applies an idempotent CreateOrUpdate to an already-provisioned
// VM. When the driver supports in-place update (AzureVMController) the existing
// instance's mutable config is overwritten and its ID preserved; otherwise the
// current instance is returned unchanged so no duplicate is created.
//
//nolint:gocritic // rp/req are request-scoped values passed once per request
func (h *Handler) updateExisting(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	req vmRequest, existing *computedriver.Instance, cfg computedriver.InstanceConfig,
) {
	if ctrl, ok := h.compute.(computedriver.AzureVMController); ok {
		if err := ctrl.UpdateInstance(r.Context(), existing.ID, cfg); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		updated, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		existing = updated
	}

	// A re-PUT ensures the OS disk exists (idempotent: a no-op when already
	// materialized, aside from refreshing its deleteOption).
	if err := h.materializeOSDisk(r.Context(), rp, existing.ID, req.Properties.StorageProfile); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// storageProfile.dataDisks is the VM's complete desired data-disk state on
	// PUT: materialize+attach newly-referenced disks and detach any previously
	// attached disk whose LUN is no longer present in the request.
	if err := h.applyDataDisks(r.Context(), rp, existing.ID, dataDisksOf(req.Properties.StorageProfile), true); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.buildVMResponse(r.Context(), existing, rp, req, provisioningSucceeded))
}

// update handles PATCH virtualMachines/{name} — ARM's BeginUpdate. Unlike PUT,
// Update is a merge-patch (RFC 7386): only the fields present in the body are
// applied and everything else is left untouched. It applies the modeled
// mutable fields a PATCH may carry — hardwareProfile.vmSize (resize), tags
// (merged into the existing set), identity — via the driver's PatchInstance,
// which leaves omitted fields (priority, licenseType, existing tags, …) intact,
// rather than routing through UpdateInstance (whose full cfg-replace assumes
// PUT's whole-body shape and would blank what a partial PATCH omits). Unmodeled
// request props are preserved by the echo overlay; storageProfile.dataDisks is
// reconciled below.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req vmRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Apply the modeled mutable fields the PATCH supplied (vmSize/tags/identity),
	// merging into the existing VM rather than replacing its whole config.
	if ctrl, ok := h.compute.(computedriver.AzureVMController); ok {
		patch := computedriver.AzureVMPatch{
			VMSize:   hardwareSize(req.Properties.HardwareProfile),
			Tags:     req.Tags,
			Identity: toDriverIdentity(req.Identity),
		}

		if err = ctrl.PatchInstance(r.Context(), inst.ID, patch); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	// A PATCH that omits storageProfile.dataDisks entirely (nil slice) leaves the
	// attached disks untouched — true merge-patch. But a PATCH that *supplies* a
	// dataDisks array (non-nil, empty included) is a full replace of the disk set:
	// real Azure detaches every disk whose LUN is absent from the array (this is
	// how `az vm update` / BeginUpdate remove a data disk). json.Unmarshal yields
	// nil for an omitted array and a non-nil (possibly empty) slice for a present
	// one, so presence is a sufficient declarative-vs-merge discriminator.
	if err = h.applyDataDisks(
		r.Context(), rp, inst.ID, dataDisksOf(req.Properties.StorageProfile), dataDisksPresent(req.Properties.StorageProfile),
	); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	updated, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.buildVMResponse(r.Context(), updated, rp, req, provisioningSucceeded))
}

// dataDisksOf safely reads storageProfile.dataDisks, tolerating a request
// whose storageProfile was omitted entirely.
func dataDisksOf(s *storageProfile) []dataDisk {
	if s == nil {
		return nil
	}

	return s.DataDisks
}

// dataDisksPresent reports whether the request explicitly supplied a
// storageProfile.dataDisks array (non-nil, empty included) — the signal that a
// PATCH is a full replace of the data-disk set rather than a merge-patch that
// omitted the field. It stays false when storageProfile or its dataDisks array
// was absent, so an omitted array leaves attachments untouched.
func dataDisksPresent(s *storageProfile) bool {
	return s != nil && s.DataDisks != nil
}

// applyDataDisks reconciles instanceID's attached data disks against disks,
// the request's storageProfile.dataDisks. declarative selects full-replace
// semantics — the array is the VM's complete desired data-disk state, so any
// currently attached disk whose LUN is absent from disks is detached — versus
// merge-patch semantics, where a disk is detached only when its own entry sets
// toBeDetached and entries absent from the request are left untouched. PUT
// CreateOrUpdate always uses full-replace; PATCH Update uses full-replace when
// the request supplies a dataDisks array and merge-patch when it omits one.
// Both modes attach any entry whose managedDisk.id resolves to a disk not yet
// attached at that LUN, and materialize a brand-new managed disk for any entry
// whose createOption is implicit (Empty/FromImage/Copy/Restore).
//
//nolint:gocritic // rp is a request-scoped value threaded through the disk apply chain.
func (h *Handler) applyDataDisks(
	ctx context.Context, rp azurearm.ResourcePath, instanceID string, disks []dataDisk, declarative bool,
) error {
	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return err
	}

	attached := attachedDisksByLUN(vols, instanceID)
	seen := make(map[int]bool, len(disks))

	for i := range disks {
		seen[disks[i].Lun] = true

		if err := h.applyDataDisk(ctx, rp, instanceID, &disks[i], vols, attached); err != nil {
			return err
		}
	}

	if !declarative {
		return nil
	}

	return detachUnlistedDisks(ctx, h.compute, attached, seen)
}

// applyDataDisk applies one storageProfile.dataDisks entry: detaches the disk
// currently attached at d.Lun when d.ToBeDetached is set, otherwise attaches the
// disk d references (createOption Attach) or materializes+attaches a brand-new
// managed disk (createOption Empty/FromImage/Copy/Restore).
//
//nolint:gocritic // rp is a request-scoped value threaded through the disk apply chain.
func (h *Handler) applyDataDisk(
	ctx context.Context, rp azurearm.ResourcePath, instanceID string,
	d *dataDisk, vols []computedriver.VolumeInfo, attached map[int]string,
) error {
	if d.ToBeDetached {
		volID, ok := attached[d.Lun]
		if !ok {
			return nil
		}

		if err := h.compute.DetachVolume(ctx, volID, "", ""); err != nil {
			return err
		}

		delete(attached, d.Lun)

		return nil
	}

	if isImplicitCreateOption(d.CreateOption) {
		return h.attachImplicitDataDisk(ctx, rp, instanceID, d, attached)
	}

	return h.attachExistingDataDisk(ctx, instanceID, d, vols, attached)
}

// isImplicitCreateOption reports whether a dataDisk/osDisk createOption
// provisions a brand-new managed disk (as opposed to Attach, which references an
// existing one). An empty/unknown createOption is not implicit, so such an entry
// is skipped rather than materialized.
func isImplicitCreateOption(opt string) bool {
	switch strings.ToLower(opt) {
	case "empty", "fromimage", "copy", "restore":
		return true
	default:
		return false
	}
}

// attachImplicitDataDisk materializes a brand-new managed disk for an implicit
// createOption (Empty/FromImage/Copy/Restore) and attaches it at d.Lun. It is
// idempotent: when a disk is already attached at d.Lun (e.g. a re-PUT re-sending
// the same implicit entry) it re-materializes nothing and only refreshes the
// attachment's deleteOption, so a repeated PUT does not pile up phantom disks.
//
//nolint:gocritic // rp is a request-scoped value threaded through the disk apply chain.
func (h *Handler) attachImplicitDataDisk(
	ctx context.Context, rp azurearm.ResourcePath, instanceID string, d *dataDisk, attached map[int]string,
) error {
	if volID, ok := attached[d.Lun]; ok {
		return h.applyDiskDeleteOption(ctx, volID, d.DeleteOption)
	}

	name := d.Name
	if name == "" {
		name = fmt.Sprintf("%s_datadisk_%d", rp.ResourceName, d.Lun)
	}

	vol, err := h.compute.CreateVolume(ctx, computedriver.VolumeConfig{
		Size:       d.DiskSizeGB,
		VolumeType: managedDiskStorageType(d.ManagedDisk),
		Tags:       diskMaterializeTags(name, rp.ResourceGroup, d.CreateOption),
	})
	if err != nil {
		return err
	}

	if err := h.compute.AttachVolume(ctx, vol.ID, instanceID, strconv.Itoa(d.Lun)); err != nil {
		return err
	}

	attached[d.Lun] = vol.ID

	return h.applyDiskDeleteOption(ctx, vol.ID, d.DeleteOption)
}

// attachExistingDataDisk attaches the pre-existing managed disk d references
// (createOption Attach). Real Azure's dataDisks are keyed by lun: re-declaring a
// lun with a different managedDisk.id implicitly detaches whatever disk
// currently occupies it (clearing that disk's managedBy/diskState) before
// attaching the new one, rather than mapping two disks onto one lun. When
// attached[d.Lun] already holds a different volume, we detach it first and
// update the attached bookkeeping so the rest of this reconciliation pass (and
// detachUnlistedDisks) sees the new occupant, not the stale one. A no-op when d
// resolves to "" (an empty/unknown createOption). The attachment's deleteOption
// is recorded on the disk either way, so a re-declared attachment stays in sync.
func (h *Handler) attachExistingDataDisk(
	ctx context.Context, instanceID string, d *dataDisk, vols []computedriver.VolumeInfo, attached map[int]string,
) error {
	volID, err := resolveAttachDiskID(d, vols)
	if err != nil {
		return err
	}

	if volID == "" {
		return nil
	}

	if attached[d.Lun] == volID {
		return h.applyDiskDeleteOption(ctx, volID, d.DeleteOption)
	}

	if prevVolID, ok := attached[d.Lun]; ok && prevVolID != volID {
		if err := h.compute.DetachVolume(ctx, prevVolID, "", ""); err != nil {
			return err
		}
	}

	if err := h.compute.AttachVolume(ctx, volID, instanceID, strconv.Itoa(d.Lun)); err != nil {
		return err
	}

	attached[d.Lun] = volID

	return h.applyDiskDeleteOption(ctx, volID, d.DeleteOption)
}

// applyDiskDeleteOption records an attachment's ARM deleteOption on the disk so
// VM delete can cascade (Delete → delete the disk, Detach → keep it). It maps
// deleteOption onto the shared VolumeInfo.DeleteOnTermination via the optional
// AzureDiskDeleteOptioner capability; a driver without it is a no-op.
func (h *Handler) applyDiskDeleteOption(ctx context.Context, volID, deleteOption string) error {
	optioner, ok := h.compute.(computedriver.AzureDiskDeleteOptioner)
	if !ok {
		return nil
	}

	return optioner.SetDiskDeleteOnTermination(ctx, volID, strings.EqualFold(deleteOption, deleteOptionDelete))
}

// managedDiskStorageType returns the storageAccountType (disk SKU) an ARM
// managedDisk block requests, or "" when unset (CreateVolume then defaults it).
func managedDiskStorageType(m *managedDiskParameters) string {
	if m == nil {
		return ""
	}

	return m.StorageAccountType
}

// diskMaterializeTags builds the cloudemu-internal tag set that lets the disks
// wire handler render a materialized OS/data disk as a Microsoft.Compute/disks
// resource: its ARM name, resource group, and createOption.
func diskMaterializeTags(name, resourceGroup, createOption string) map[string]string {
	tags := map[string]string{diskARMNameTag: name}

	if resourceGroup != "" {
		tags[diskRGTag] = resourceGroup
	}

	if createOption != "" {
		tags[diskCreateOptionTag] = createOption
	}

	return tags
}

// materializeOSDisk creates and attaches the VM's OS managed disk as a real
// Microsoft.Compute/disks resource from storageProfile.osDisk, so the disks API
// returns it. createOption Attach references an existing disk; Empty/FromImage
// (or an unset createOption, treated as FromImage) provisions a new one. It is
// idempotent: a re-PUT that finds the OS disk already attached only refreshes
// the attachment's deleteOption. A request with no osDisk is a no-op.
//
//nolint:gocritic // rp is a request-scoped value.
func (h *Handler) materializeOSDisk(
	ctx context.Context, rp azurearm.ResourcePath, instanceID string, sp *storageProfile,
) error {
	if sp == nil || sp.OSDisk == nil {
		return nil
	}

	od := sp.OSDisk

	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return err
	}

	if volID, ok := osDiskOf(vols, instanceID); ok {
		return h.applyDiskDeleteOption(ctx, volID, od.DeleteOption)
	}

	volID, err := h.resolveOrCreateOSDisk(ctx, rp, od, vols)
	if err != nil || volID == "" {
		return err
	}

	if err := h.compute.AttachVolume(ctx, volID, instanceID, osDiskDevice); err != nil {
		return err
	}

	return h.applyDiskDeleteOption(ctx, volID, od.DeleteOption)
}

// resolveOrCreateOSDisk resolves the volume backing the OS disk: an Attach
// createOption references an existing managed disk by managedDisk.id, while any
// other createOption (defaulting to FromImage) provisions a brand-new disk named
// {vmName}_osdisk when the request supplies no name.
//
//nolint:gocritic // rp is a request-scoped value.
func (h *Handler) resolveOrCreateOSDisk(
	ctx context.Context, rp azurearm.ResourcePath, od *osDisk, vols []computedriver.VolumeInfo,
) (string, error) {
	if strings.EqualFold(od.CreateOption, createOptionAttach) {
		if od.ManagedDisk == nil || od.ManagedDisk.ID == "" {
			return "", cerrors.New(cerrors.InvalidArgument, "osDisk createOption Attach requires managedDisk.id")
		}

		return resolveManagedDiskVolID(od.ManagedDisk.ID, vols)
	}

	name := od.Name
	if name == "" {
		name = rp.ResourceName + "_osdisk"
	}

	createOption := od.CreateOption
	if createOption == "" {
		createOption = "FromImage"
	}

	vol, err := h.compute.CreateVolume(ctx, computedriver.VolumeConfig{
		Size:       od.DiskSizeGB,
		VolumeType: managedDiskStorageType(od.ManagedDisk),
		Tags:       diskMaterializeTags(name, rp.ResourceGroup, createOption),
	})
	if err != nil {
		return "", err
	}

	return vol.ID, nil
}

// osDiskOf returns the id of the OS disk currently attached to instanceID (the
// volume attached at the osDiskDevice marker), reporting false when none is.
func osDiskOf(vols []computedriver.VolumeInfo, instanceID string) (string, bool) {
	for i := range vols {
		if vols[i].AttachedTo == instanceID && vols[i].Device == osDiskDevice {
			return vols[i].ID, true
		}
	}

	return "", false
}

// detachUnlistedDisks detaches every attached disk whose LUN is absent from
// seen — the declarative-PUT half of applyDataDisks' reconciliation.
func detachUnlistedDisks(ctx context.Context, c computedriver.Compute, attached map[int]string, seen map[int]bool) error {
	for lun, volID := range attached {
		if seen[lun] {
			continue
		}

		if err := c.DetachVolume(ctx, volID, "", ""); err != nil {
			return err
		}
	}

	return nil
}

// attachedDisksByLUN indexes the volumes currently attached to instanceID by
// the LUN AttachVolume was called with (recovered from the driver's Device
// field), for reconciling against a request's desired dataDisks.
func attachedDisksByLUN(vols []computedriver.VolumeInfo, instanceID string) map[int]string {
	byLUN := make(map[int]string)

	for i := range vols {
		if vols[i].AttachedTo != instanceID {
			continue
		}

		if lun, ok := parseLUN(vols[i].Device); ok {
			byLUN[lun] = vols[i].ID
		}
	}

	return byLUN
}

// parseLUN recovers the integer LUN AttachVolume was called with from a
// volume's driver Device field (applyDataDisks passes strconv.Itoa(lun) as
// the device on attach). A non-numeric Device reports false so the volume is
// excluded from LUN-keyed reconciliation.
func parseLUN(device string) (int, bool) {
	lun, err := strconv.Atoi(device)
	if err != nil {
		return 0, false
	}

	return lun, true
}

// resolveAttachDiskID resolves the volume ID a dataDisk entry's managedDisk.id
// references. Only createOption "Attach" references an existing managed disk;
// implicit createOptions (Empty/FromImage/Copy/Restore) are materialized
// elsewhere (attachImplicitDataDisk), so this resolves them to "" and they are
// skipped here rather than erroring.
func resolveAttachDiskID(d *dataDisk, vols []computedriver.VolumeInfo) (string, error) {
	if !strings.EqualFold(d.CreateOption, createOptionAttach) {
		return "", nil
	}

	if d.ManagedDisk == nil || d.ManagedDisk.ID == "" {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"dataDisk lun %d: createOption Attach requires managedDisk.id", d.Lun)
	}

	return resolveManagedDiskVolID(d.ManagedDisk.ID, vols)
}

// resolveManagedDiskVolID finds the driver volume backing an ARM managed-disk id
// (matched by its azureDiskName / azureRG tags). Returns NotFound when no volume
// carries that name, InvalidArgument when the id is malformed.
func resolveManagedDiskVolID(managedDiskID string, vols []computedriver.VolumeInfo) (string, error) {
	pp, ok := azurearm.ParsePath(managedDiskID)
	if !ok || pp.ResourceName == "" {
		return "", cerrors.Newf(cerrors.InvalidArgument, "malformed managed disk id %q", managedDiskID)
	}

	for i := range vols {
		if tagOr(vols[i].Tags, diskARMNameTag, "") != pp.ResourceName {
			continue
		}

		if pp.ResourceGroup != "" && tagOr(vols[i].Tags, diskRGTag, "") != pp.ResourceGroup {
			continue
		}

		return vols[i].ID, nil
	}

	return "", cerrors.Newf(cerrors.NotFound, "managed disk %q not found", pp.ResourceName)
}

// attachedDataDisks builds the storageProfile.dataDisks response entries for
// every managed disk currently attached to instanceID, so GET/LIST/
// CreateOrUpdate/Update responses reflect real attachment state regardless of
// what a particular request body asked for.
//
//nolint:gocritic // rp is a request-scoped value; pointer chain isn't worth the noise
func (h *Handler) attachedDataDisks(ctx context.Context, rp azurearm.ResourcePath, instanceID string) []dataDisk {
	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil
	}

	out := make([]dataDisk, 0, len(vols))

	for i := range vols {
		if vols[i].AttachedTo != instanceID {
			continue
		}

		lun, ok := parseLUN(vols[i].Device)
		if !ok {
			continue
		}

		name := tagOr(vols[i].Tags, diskARMNameTag, vols[i].ID)
		diskRG := tagOr(vols[i].Tags, diskRGTag, rp.ResourceGroup)

		out = append(out, dataDisk{
			Lun:          lun,
			Name:         name,
			CreateOption: "Attach",
			DiskSizeGB:   vols[i].Size,
			ManagedDisk: &managedDiskParameters{
				ID:                 azurearm.BuildResourceID(rp.Subscription, diskRG, providerName, "disks", name),
				StorageAccountType: vols[i].VolumeType,
			},
		})
	}

	sort.Slice(out, func(a, b int) bool { return out[a].Lun < out[b].Lun })

	return out
}

// get handles GET virtualMachines/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.buildVMResponse(r.Context(), inst, rp, vmRequest{}, provisioningSucceeded))
}

// list handles GET virtualMachines. A resource-group-scoped path
// (.../resourceGroups/{rg}/...) returns only that group's VMs; a
// subscription-scoped path returns every VM.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	instances, err := h.compute.DescribeInstances(r.Context(), nil, nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]vmResponse, 0, len(instances))

	for i := range instances {
		if rp.ResourceGroup != "" && !strings.EqualFold(instances[i].ResourceGroup, rp.ResourceGroup) {
			continue
		}

		name := tagOr(instances[i].Tags, armNameTag, instances[i].ID)
		scope := rp
		// On a subscription-scoped list rp.ResourceGroup is empty; use the
		// instance's own recorded group so the rendered id carries its true
		// resourceGroups/{rg} segment (arm.ParseResourceID / Terraform state).
		scope.ResourceGroup = instances[i].ResourceGroup
		scope.ResourceName = name
		out = append(out, h.buildVMResponse(r.Context(), &instances[i], scope, vmRequest{}, provisioningSucceeded))
	}

	azurearm.WriteJSON(w, http.StatusOK, vmListResponse{Value: out})
}

// instanceView handles GET virtualMachines/{name}/instanceView. It returns the
// VirtualMachineInstanceView: the provisioning + power state statuses, the VM
// agent status, and per-disk status the Azure SDK's InstanceView call reads.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) instanceView(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toInstanceView(inst))
}

// delete handles DELETE virtualMachines/{name}. Returns a 202 Accepted with
// the async-operation polling header so the SDK's poller terminates cleanly.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// TerminateInstances cascades the VM's attached managed disks per each
	// attachment's deleteOption (recorded as VolumeInfo.DeleteOnTermination):
	// a "Delete" disk is deleted with the VM, a "Detach" disk is released
	// (returned to Unattached) rather than left dangling — matching real Azure.
	if err := h.compute.TerminateInstances(r.Context(), []string{inst.ID}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "delete-"+rp.ResourceName)
}

// PurgeResourceGroup terminates every virtual machine — and deletes every
// virtual machine scale set (via purgeScaleSets) — created under the given
// resource group, backing the resource-group cascade delete. Instances record
// their group (Instance.ResourceGroup) on the ARM create path, so membership is
// an exact match. Resource-group comparison is case-insensitive, matching ARM.
// The subscription is unused (the emulator is single-estate).
//
// Each VM's attached managed disks cascade per their deleteOption inside
// TerminateInstances — the same release the single-VM delete() path relies on —
// so a cascade-deleted VM deletes its "Delete" disks and releases its "Detach"
// disks (returned to Unattached) rather than leaving them dangling at the
// now-deleted VM.
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	var firstErr error

	if err := h.purgeInstances(ctx, resourceGroup); err != nil {
		firstErr = err
	}

	if serr := h.purgeScaleSets(ctx, resourceGroup); serr != nil && firstErr == nil {
		firstErr = serr
	}

	return firstErr
}

// purgeInstances terminates every virtual machine under the given resource
// group — the VM half of the cascade. TerminateInstances cascades each VM's
// attached disks per their deleteOption.
func (h *Handler) purgeInstances(ctx context.Context, resourceGroup string) error {
	instances, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(instances))

	for i := range instances {
		if strings.EqualFold(instances[i].ResourceGroup, resourceGroup) {
			ids = append(ids, instances[i].ID)
		}
	}

	if len(ids) == 0 {
		return nil
	}

	return h.compute.TerminateInstances(ctx, ids)
}

// purgeScaleSets deletes every virtual machine scale set under the given
// resource group, so an RG cascade tears down its scale sets too (matching the
// VM teardown above). A driver that does not implement the scale-set store is a
// no-op. Resource-group comparison is case-insensitive, matching ARM.
func (h *Handler) purgeScaleSets(ctx context.Context, resourceGroup string) error {
	store, ok := h.compute.(scaleSetStore)
	if !ok {
		return nil
	}

	sets, err := store.ListScaleSets(ctx)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range sets {
		if !strings.EqualFold(sets[i].ResourceGroup, resourceGroup) {
			continue
		}

		if derr := store.DeleteScaleSet(ctx, sets[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

// start handles POST virtualMachines/{name}/start.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) start(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.StartInstances)
}

// powerOff handles POST virtualMachines/{name}/powerOff. It stops the guest OS
// while keeping the VM allocated (PowerState/stopped).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) powerOff(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.powerAction(w, r, rp, func(ctrl computedriver.AzureVMController, id string) error {
		return ctrl.PowerOff(r.Context(), id)
	})
}

// deallocate handles POST virtualMachines/{name}/deallocate. It stops the guest
// and releases the allocated compute (PowerState/deallocated).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deallocate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.powerAction(w, r, rp, func(ctrl computedriver.AzureVMController, id string) error {
		return ctrl.Deallocate(r.Context(), id)
	})
}

// powerAction is the shared body for powerOff/deallocate. It invokes the Azure
// power controller when the driver supports it (preserving the PowerOff vs
// Deallocate distinction) and otherwise falls back to the generic StopInstances.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) powerAction(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	op func(ctrl computedriver.AzureVMController, id string) error,
) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if ctrl, ok := h.compute.(computedriver.AzureVMController); ok {
		err = op(ctrl, inst.ID)
	} else {
		err = h.compute.StopInstances(r.Context(), []string{inst.ID})
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, rp.SubResource+"-"+rp.ResourceName)
}

// restart handles POST virtualMachines/{name}/restart.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) restart(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.RebootInstances)
}

// redeploy handles POST virtualMachines/{name}/redeploy. Real Azure powers the
// VM off, relocates it to a fresh host, and powers it back on; the net
// observable effect is a brief power cycle that leaves the VM running with
// provisioningState Succeeded and no data loss. We model that by power-cycling
// the VM back to the running state, reusing the existing lifecycle plumbing so
// the same running metrics are emitted.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) redeploy(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.powerCycleToRunning(w, r, rp)
}

// reimage handles POST virtualMachines/{name}/reimage. Real Azure reinstalls
// the OS by resetting the OS disk to its original image and leaves the VM
// running. We model the observable power/state transition (VM ends running,
// provisioningState Succeeded); OS-disk-reset fidelity is deferred to the Azure
// OS-disk PR, which is the work that first materializes the OS disk as a
// tracked resource for the emulator to reset.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) reimage(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.powerCycleToRunning(w, r, rp)
}

// powerCycleToRunning is the shared body for redeploy/reimage: it takes the VM
// through a power cycle that ends in the running state. A running VM is
// rebooted (running→restarting→running); a stopped/deallocated one is started.
// Returns 202 + async polling header so real Azure SDK pollers terminate.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) powerCycleToRunning(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if powerCode(inst) == stateRunning {
		err = h.compute.RebootInstances(r.Context(), []string{inst.ID})
	} else {
		err = h.compute.StartInstances(r.Context(), []string{inst.ID})
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, rp.SubResource+"-"+rp.ResourceName)
}

// generalize handles POST virtualMachines/{name}/generalize. It marks the VM
// as generalized (OS-specific state removed), a precondition for capturing it
// into a reusable image. Real Azure returns 200 OK with no body.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) generalize(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	ctrl, ok := h.compute.(computedriver.AzureVMController)
	if !ok {
		writeNotImplemented(w, "action: generalize")
		return
	}

	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Real Azure requires the VM to be stopped or deallocated before it can be
	// generalized; generalizing a running (or otherwise not-stopped) VM is
	// rejected with OperationNotAllowed / 409 Conflict.
	if power := powerCode(inst); power != powerCodeStopped && power != powerCodeDeallocated {
		azurearm.WriteError(w, http.StatusConflict, "OperationNotAllowed",
			"the virtual machine must be stopped or deallocated before it can be generalized")

		return
	}

	if err := ctrl.GeneralizeInstance(r.Context(), inst.ID); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// capture handles POST virtualMachines/{name}/capture. It copies the VM's
// virtual hard disks and returns a template (VirtualMachineCaptureResult) that
// can recreate similar VMs. The VM must first be generalized — capturing a
// non-generalized VM is rejected, matching real Azure.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) capture(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	var req captureRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if !inst.Generalized {
		azurearm.WriteError(w, http.StatusConflict, "OperationNotAllowed",
			"the virtual machine must be generalized before it can be captured")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, captureResult(inst, rp, req))
}

// captureResult builds the VirtualMachineCaptureResult template for a captured
// VM: the ARM deployment-template envelope plus a single virtualMachines/image
// resource describing the captured VHD.
//
//nolint:gocritic // rp/req are request-scoped values
func captureResult(inst *computedriver.Instance, rp azurearm.ResourcePath, req captureRequest) captureResponse {
	prefix := req.VhdPrefix
	if prefix == "" {
		prefix = "captured"
	}

	vhdURI := "https://cloudemu.blob.storage.azure.net/" +
		req.DestinationContainerName + "/" + prefix + "-osdisk.vhd"

	return captureResponse{
		Schema:         "http://schema.management.azure.com/schemas/2015-01-01/deploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters:     map[string]any{},
		ID:             azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName),
		Resources: []captureResource{{
			Type:       providerName + "/images",
			APIVersion: "2023-09-01",
			Properties: captureResourceProps{
				StorageProfile: captureStorageProfile{
					OSDisk: captureOSDisk{
						OSType: defaultIfEmpty(inst.OSType, "Linux"),
						Name:   prefix + "-osdisk",
						Image:  &captureVHD{URI: vhdURI},
					},
				},
			},
		}},
	}
}

// retrieveBootDiagnosticsData handles POST virtualMachines/{name}/retrieveBootDiagnosticsData.
// It mirrors the real ARM action: rather than returning the serial-log bytes
// inline, it returns the URIs a client downloads the console screenshot and
// serial log from. We point the serial-log URI back at this server's
// bootDiagnostics/serialConsoleLog GET so the engine-captured boot output is
// retrievable (the closest faithful analog to the real Azure Storage SAS blob).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) retrieveBootDiagnosticsData(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if _, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, bootDiagnosticsDataResult{
		SerialConsoleLogBlobURI: serialConsoleLogURI(r, rp),
	})
}

// serialConsoleLog handles GET virtualMachines/{name}/bootDiagnostics/serialConsoleLog,
// the endpoint retrieveBootDiagnosticsData points its serialConsoleLogBlobUri at.
// It returns the raw console bytes the compute engine captured for the VM's boot
// script (empty when no engine backs it).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serialConsoleLog(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	reader, ok := h.compute.(computedriver.ConsoleReader)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "boot diagnostics not supported")
		return
	}

	out, err := reader.GetConsoleOutput(r.Context(), inst.ID)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// serialConsoleLogURI builds the absolute URL of this VM's serial-log download
// endpoint, resolved against the incoming request's host and scheme so it works
// on both the http and https (TLS) listeners.
//
//nolint:gocritic // rp is a request-scoped value
func serialConsoleLogURI(r *http.Request, rp azurearm.ResourcePath) string {
	return requestScheme(r) + "://" + r.Host +
		azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName) +
		"/bootDiagnostics/serialConsoleLog?api-version=2023-09-01"
}

// lifecycleAction is the shared body for start/stop/restart. Returns 202
// + async polling header so real Azure SDK pollers terminate.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) lifecycleAction(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	op func(ctx context.Context, ids []string) error,
) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := op(r.Context(), []string{inst.ID}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, rp.SubResource+"-"+rp.ResourceName)
}

// writeAcceptedAsync replies 202 Accepted with the Azure-AsyncOperation and
// Location headers pointing to our operationStatuses endpoint. The Azure SDK
// poller will GET that URL and observe Succeeded immediately.
func writeAcceptedAsync(w http.ResponseWriter, r *http.Request, subscription, opID string) {
	statusURL := requestScheme(r) + "://" + r.Host +
		"/subscriptions/" + subscription +
		"/providers/" + providerName +
		"/locations/eastus/operationStatuses/" + opID +
		"?api-version=2023-09-01"

	w.Header().Set("Azure-AsyncOperation", statusURL)
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

// findByName looks up a VM by its ARM resource name within a resource group.
// An empty resourceGroup matches across all groups (subscription-scoped lookup).
// Returns NotFound when no matching instance exists.
func findByName(ctx context.Context, c computedriver.Compute, resourceGroup, name string) (*computedriver.Instance, error) {
	instances, err := c.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	for i := range instances {
		// ARM resource names and resource-group names are case-insensitive, so a
		// GET/LIST with differently-cased segments must still resolve the VM.
		if !strings.EqualFold(tagOr(instances[i].Tags, armNameTag, ""), name) {
			continue
		}

		if resourceGroup != "" && !strings.EqualFold(instances[i].ResourceGroup, resourceGroup) {
			continue
		}

		return &instances[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "virtualMachine %s not found", name)
}

// Helpers that map ARM JSON shapes to/from the driver model.

func imageRefToID(s *storageProfile) string {
	if s == nil || s.ImageReference == nil {
		return ""
	}

	if s.ImageReference.ID != "" {
		return s.ImageReference.ID
	}

	parts := []string{s.ImageReference.Publisher, s.ImageReference.Offer, s.ImageReference.SKU, s.ImageReference.Version}

	return strings.Trim(strings.Join(parts, ":"), ":")
}

func hardwareSize(p *hardwareProfile) string {
	if p == nil {
		return ""
	}

	return p.VMSize
}

func firstNicID(n *networkProfile) string {
	if n == nil || len(n.NetworkInterfaces) == 0 {
		return ""
	}

	return n.NetworkInterfaces[0].ID
}

func computerName(o *osProfile) string {
	if o == nil {
		return ""
	}

	return o.ComputerName
}

func customData(o *osProfile) string {
	if o == nil {
		return ""
	}

	return o.CustomData
}

// decodeCustomData decodes the base64 customData Azure carries on osProfile.
// Real ARM customData is always base64-encoded; a value that does not decode is
// passed through raw so a client that sent plain text still gets its boot script.
func decodeCustomData(s string) string {
	if s == "" {
		return ""
	}

	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(decoded)
	}

	return s
}

// toDriverIdentity maps an inbound ARM identity block onto the driver shape.
// Only Type and the UserAssignedIdentities keys (the identity resource IDs to
// attach) are meaningful on input; principalId/tenantId/clientId are
// read-only and decoded but discarded. Returns nil when the request did not
// include an identity block at all (the caller then leaves any existing
// identity untouched).
func toDriverIdentity(in *identity) *computedriver.ManagedIdentity {
	if in == nil {
		return nil
	}

	out := &computedriver.ManagedIdentity{Type: in.Type}

	if len(in.UserAssignedIdentities) > 0 {
		out.UserAssigned = make(map[string]computedriver.UserAssignedIdentity, len(in.UserAssignedIdentities))
		for id := range in.UserAssignedIdentities {
			out.UserAssigned[id] = computedriver.UserAssignedIdentity{}
		}
	}

	return out
}

// fromDriverIdentity builds the ARM identity response block from the
// resolved driver shape, echoing each user-assigned identity's synthesized
// principalId/clientId. Returns nil when the instance has no identity
// attached, so the response omits the field entirely (matching real Azure).
func fromDriverIdentity(in *computedriver.ManagedIdentity) *identity {
	if in == nil {
		return nil
	}

	out := &identity{Type: in.Type, PrincipalID: in.PrincipalID, TenantID: in.TenantID}

	if len(in.UserAssigned) > 0 {
		out.UserAssignedIdentities = make(map[string]*userAssignedIdentity, len(in.UserAssigned))
		for id, u := range in.UserAssigned {
			out.UserAssignedIdentities[id] = &userAssignedIdentity{PrincipalID: u.PrincipalID, ClientID: u.ClientID}
		}
	}

	return out
}

func osTypeFromStorage(s *storageProfile) string {
	if s == nil || s.OSDisk == nil {
		return ""
	}

	return s.OSDisk.OSType
}

func mergeTags(in map[string]string, armName string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = armName

	return out
}

// tagOr returns the tag value for key, or fallback when absent.
func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

// ARM provisioningState values a VM settles through. A create returns
// "Creating"; a subsequent GET/poll reports "Succeeded".
const (
	provisioningCreating  = "Creating"
	provisioningSucceeded = "Succeeded"
)

// buildVMResponse builds the ARM vmResponse for inst, augmenting
// storageProfile.dataDisks with the disks actually attached to it (via
// attachedDataDisks) so every response reflects real attachment state —
// including a disk attached by an earlier request, not just the one that
// produced this particular response.
//
//nolint:gocritic // rp/req are value types passed once per response build
func (h *Handler) buildVMResponse(
	ctx context.Context, inst *computedriver.Instance, rp azurearm.ResourcePath, req vmRequest, provisioningState string,
) vmResponse {
	resp := toVMResponse(inst, rp, req, provisioningState)

	if disks := h.attachedDataDisks(ctx, rp, inst.ID); len(disks) > 0 {
		if resp.Properties.StorageProfile == nil {
			resp.Properties.StorageProfile = &storageProfile{}
		}

		resp.Properties.StorageProfile.DataDisks = disks
	}

	return resp
}

// toVMResponse maps a driver Instance back onto the ARM JSON shape.
// provisioningState is the properties.provisioningState to report ("Creating"
// on the initial create response, "Succeeded" thereafter).
//
//nolint:gocritic // rp/req are value types passed once per response build
func toVMResponse(inst *computedriver.Instance, rp azurearm.ResourcePath, req vmRequest, provisioningState string) vmResponse {
	name := tagOr(inst.Tags, armNameTag, rp.ResourceName)

	return vmResponse{
		ID:   azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name: name,
		Type: providerName + "/" + resourceType,
		// Prefer the instance's recorded region so GET/LIST report where the VM
		// was actually created; the request location (create path) and the
		// "eastus" default are fallbacks for instances that carry no region.
		Location: defaultIfEmpty(inst.Region, defaultIfEmpty(req.Location, "eastus")),
		Tags:     stripInternalTags(inst.Tags),
		Zones:    inst.Zones,
		Identity: fromDriverIdentity(inst.Identity),
		Properties: vmResponseProps{
			VMID:              inst.ID,
			ProvisioningState: provisioningState,
			HardwareProfile:   &hardwareProfile{VMSize: inst.InstanceType},
			StorageProfile:    osDiskProfile(inst.OSType),
			Priority:          inst.Priority,
			LicenseType:       inst.LicenseType,
			InstanceView: &instanceView{
				Statuses: []instanceViewStatus{
					provisioningStatus(provisioningState),
					{Code: "PowerState/" + powerCode(inst), Level: "Info", DisplayStatus: powerDisplay(inst)},
				},
			},
		},
	}
}

// provisioningStatus renders the instanceView ProvisioningState status line for
// the given provisioningState.
func provisioningStatus(state string) instanceViewStatus {
	return instanceViewStatus{
		Code:          "ProvisioningState/" + strings.ToLower(state),
		Level:         "Info",
		DisplayStatus: "Provisioning " + strings.ToLower(state),
	}
}

// osDiskProfile echoes the guest OS family under the storageProfile.osDisk
// path the SDK reads it from. Returns nil when the OS type is unknown so the
// field is omitted rather than emitted empty.
func osDiskProfile(osType string) *storageProfile {
	if osType == "" {
		return nil
	}

	return &storageProfile{OSDisk: &osDisk{OSType: osType}}
}

func defaultIfEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
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

// powerCode returns the ARM PowerState code suffix for an instance. It prefers
// the explicit Azure power state (which distinguishes stopped from deallocated)
// and falls back to a lifecycle-state mapping when unset.
func powerCode(inst *computedriver.Instance) string {
	if inst.PowerState != "" {
		return inst.PowerState
	}

	return powerStateFor(inst.State)
}

func powerDisplay(inst *computedriver.Instance) string {
	return "VM " + powerCode(inst)
}

func powerStateFor(state string) string {
	switch state {
	case stateRunning:
		return "running"
	case statePending:
		return "starting"
	case stateStopped:
		return powerCodeDeallocated
	case stateStopping:
		return "deallocating"
	case stateTerminated:
		return "deleted"
	default:
		return state
	}
}

// toInstanceView builds the VirtualMachineInstanceView response for an instance.
func toInstanceView(inst *computedriver.Instance) instanceViewResponse {
	return instanceViewResponse{
		ComputerName:     tagOr(inst.Tags, armNameTag, ""),
		OSName:           osNameFor(inst.OSType),
		VMAgent:          &vmAgentInstanceView{VMAgentVersion: "2.0.0.0"},
		Disks:            []diskInstanceView{{Name: "osdisk"}},
		HyperVGeneration: "V1",
		Statuses: []instanceViewStatus{
			{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
			{Code: "PowerState/" + powerCode(inst), Level: "Info", DisplayStatus: powerDisplay(inst)},
		},
	}
}

// osNameFor renders a plausible OS name from the guest OS family, for the
// instanceView's osName field. Empty OS type yields an empty name.
func osNameFor(osType string) string {
	switch strings.ToLower(osType) {
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return osType
	}
}
