package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// newBootVolume stores the volume an instance boots from. The caller holds
// m.mu.
func (m *Mock) newBootVolume(ad, imageID, displayName string) string {
	id := m.newOCID(typeBootVolume)
	bv := &BootVolume{
		ID:                 id,
		AvailabilityDomain: ad,
		DisplayName:        bootVolumeName(displayName),
		SizeInGBs:          defaultBootVolumeGBs,
		VpusPerGB:          defaultVpusPerGB,
		ImageID:            imageID,
		SourceDetails:      SourceDetails{SourceType: sourceImage, ID: imageID},
		LifecycleState:     StateAvailable,
		IsHydrated:         true,
		TimeCreated:        m.now(),
		Tags:               map[string]string{},
	}

	m.bootVolumes.Set(id, bv)
	m.record(id)

	return id
}

// attachBootVolume ties a boot volume to the instance booting from it. The
// caller holds m.mu.
func (m *Mock) attachBootVolume(instanceID, bootVolumeID, ad string) string {
	id := m.newOCID(typeBootVolumeAttachment)
	a := &BootVolumeAttachment{
		ID:                 id,
		InstanceID:         instanceID,
		BootVolumeID:       bootVolumeID,
		AvailabilityDomain: ad,
		LifecycleState:     StateAttached,
		TimeCreated:        m.now(),
	}

	m.bootAttach.Set(id, a)
	m.record(id)

	return id
}

// addVNICAttachment records the attachment holding the VNIC the VCN service
// created for an instance. The caller holds m.mu.
//
//nolint:gocritic // hugeParam: placement is the value type being recorded.
func (m *Mock) addVNICAttachment(instanceID string, at placement, displayName string) string {
	id := at.AttachmentID
	if id == "" {
		id = m.newOCID(typeVNICAttachment)
	}

	a := &VNICAttachment{
		ID:                 id,
		InstanceID:         instanceID,
		VNICID:             at.VNICID,
		SubnetID:           at.SubnetID,
		AvailabilityDomain: m.defaultAD(),
		DisplayName:        displayName,
		LifecycleState:     StateAttached,
		TimeCreated:        m.now(),
	}

	m.vnicAttach.Set(id, a)
	m.record(id)

	return id
}

// CreateBootVolume creates a standalone boot volume, cloned from another boot
// volume or restored from a backup.
//
//nolint:gocritic // hugeParam: BootVolume is the value type being stored.
func (m *Mock) CreateBootVolume(_ context.Context, spec BootVolume) (*BootVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBootSource(spec.SourceDetails); err != nil {
		return nil, err
	}

	id := m.newOCID(typeBootVolume)
	bv := spec
	bv.ID = id
	bv.LifecycleState = StateAvailable
	bv.IsHydrated = true
	bv.TimeCreated = m.now()
	bv.Tags = copyTags(spec.Tags)

	if bv.AvailabilityDomain == "" {
		bv.AvailabilityDomain = m.defaultAD()
	}

	if bv.SizeInGBs == 0 {
		bv.SizeInGBs = defaultBootVolumeGBs
	}

	if bv.VpusPerGB == 0 {
		bv.VpusPerGB = defaultVpusPerGB
	}

	m.bootVolumes.Set(id, &bv)
	m.record(id)

	out := bv

	return &out, nil
}

// validateBootSource checks a boot volume's source exists. The caller holds m.mu.
func (m *Mock) validateBootSource(source SourceDetails) error {
	switch source.SourceType {
	case "":
		return nil
	case sourceBootVolume:
		if !m.bootVolumes.Has(source.ID) {
			return notFoundf("boot volume %q not found", source.ID)
		}
	case sourceBootVolumeBackup, sourceVolumeBackup:
		if !m.backups.Has(source.ID) {
			return notFoundf("boot volume backup %q not found", source.ID)
		}
	case sourceImage:
		if !m.images.Has(source.ID) {
			return notFoundf("image %q not found", source.ID)
		}
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported boot volume source type %q", source.SourceType)
	}

	return nil
}

// GetBootVolume returns one boot volume.
func (m *Mock) GetBootVolume(_ context.Context, id string) (*BootVolume, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bv, ok := m.bootVolumes.Get(id)
	if !ok {
		return nil, bootVolumeNotFound(id)
	}

	out := *bv

	return &out, nil
}

// ListBootVolumes returns the boot volumes in a compartment.
func (m *Mock) ListBootVolumes(_ context.Context, compartmentID string) ([]BootVolume, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listScoped(m, m.bootVolumes, compartmentID,
		func(bv *BootVolume) string { return bv.ID }, nil), nil
}

// UpdateBootVolume changes a boot volume's display name, size and tags.
func (m *Mock) UpdateBootVolume(_ context.Context, id string, upd Update, sizeInGBs, vpusPerGB int) (
	*BootVolume, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	bv, ok := m.bootVolumes.Get(id)
	if !ok {
		return nil, bootVolumeNotFound(id)
	}

	if sizeInGBs != 0 && sizeInGBs < bv.SizeInGBs {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"boot volume %q cannot shrink from %d to %d GBs", id, bv.SizeInGBs, sizeInGBs)
	}

	m.bootVolumes.Update(id, func(v *BootVolume) *BootVolume {
		if upd.DisplayName != nil {
			v.DisplayName = *upd.DisplayName
		}

		if upd.Tags != nil {
			v.Tags = mergeTags(v.Tags, upd.Tags)
		}

		if sizeInGBs != 0 {
			v.SizeInGBs = sizeInGBs
		}

		if vpusPerGB != 0 {
			v.VpusPerGB = vpusPerGB
		}

		return v
	})

	updated, _ := m.bootVolumes.Get(id)
	out := *updated

	return &out, nil
}

// DeleteBootVolume deletes a boot volume, refusing while an instance is
// attached to it.
func (m *Mock) DeleteBootVolume(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.bootVolumes.Has(id) {
		return bootVolumeNotFound(id)
	}

	for _, a := range m.bootAttach.All() {
		if a.BootVolumeID == id {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"boot volume %q is still attached to instance %q", id, a.InstanceID)
		}
	}

	m.bootVolumes.Delete(id)
	m.forget(id)

	return nil
}

// ListBootVolumeAttachments returns the boot volume attachments in a
// compartment, narrowed to an instance or a boot volume when either is named.
func (m *Mock) ListBootVolumeAttachments(_ context.Context, compartmentID, instanceID, bootVolumeID string) (
	[]BootVolumeAttachment, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listScoped(m, m.bootAttach, compartmentID,
		func(a *BootVolumeAttachment) string { return a.ID },
		func(a *BootVolumeAttachment) bool {
			return matchesBoth(instanceID, a.InstanceID, bootVolumeID, a.BootVolumeID)
		}), nil
}

// GetBootVolumeAttachment returns one boot volume attachment.
func (m *Mock) GetBootVolumeAttachment(_ context.Context, id string) (*BootVolumeAttachment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.bootAttach.Get(id)
	if !ok {
		return nil, notFoundf("boot volume attachment %q not found", id)
	}

	out := *a

	return &out, nil
}

// AttachBootVolume attaches a boot volume to a stopped instance.
func (m *Mock) AttachBootVolume(_ context.Context, instanceID, bootVolumeID, displayName string) (
	*BootVolumeAttachment, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return nil, instanceNotFound(instanceID)
	}

	if !m.bootVolumes.Has(bootVolumeID) {
		return nil, bootVolumeNotFound(bootVolumeID)
	}

	for _, a := range m.bootAttach.All() {
		if a.InstanceID == instanceID {
			return nil, cerrors.Newf(cerrors.AlreadyExists,
				"instance %q already has boot volume %q attached", instanceID, a.BootVolumeID)
		}
	}

	id := m.attachBootVolume(instanceID, bootVolumeID, inst.AD)
	a, _ := m.bootAttach.Get(id)
	a.DisplayName = displayName
	out := *a

	return &out, nil
}

// DetachBootVolume detaches a boot volume attachment.
func (m *Mock) DetachBootVolume(_ context.Context, attachmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.bootAttach.Has(attachmentID) {
		return notFoundf("boot volume attachment %q not found", attachmentID)
	}

	m.bootAttach.Delete(attachmentID)
	m.forget(attachmentID)

	return nil
}

// ListVNICAttachments returns the VNIC attachments in a compartment, narrowed
// to an instance or a VNIC when either is named.
func (m *Mock) ListVNICAttachments(_ context.Context, compartmentID, instanceID, vnicID string) (
	[]VNICAttachment, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listScoped(m, m.vnicAttach, compartmentID,
		func(a *VNICAttachment) string { return a.ID },
		func(a *VNICAttachment) bool {
			return matchesBoth(instanceID, a.InstanceID, vnicID, a.VNICID)
		}), nil
}

// GetVNICAttachment returns one VNIC attachment.
func (m *Mock) GetVNICAttachment(_ context.Context, id string) (*VNICAttachment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.vnicAttach.Get(id)
	if !ok {
		return nil, notFoundf("VNIC attachment %q not found", id)
	}

	out := *a

	return &out, nil
}

// AttachVNIC adds a secondary VNIC to an instance, creating it in the VCN
// service and recording the attachment.
func (m *Mock) AttachVNIC(ctx context.Context, instanceID, subnetID, displayName, hostname string, nsgIDs []string) (
	*VNICAttachment, error,
) {
	net := m.networking()
	if net == nil {
		return nil, cerrors.New(cerrors.Unimplemented, "no VCN service is wired; a VNIC cannot be created")
	}

	if _, err := m.instanceAD(instanceID); err != nil {
		return nil, err
	}

	at, err := m.place(ctx, net, subnetID, displayName, hostname, nsgIDs, nil)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(instanceID) {
		return nil, instanceNotFound(instanceID)
	}

	id := m.addVNICAttachment(instanceID, at, displayName)
	a, _ := m.vnicAttach.Get(id)
	a.NICIndex = m.nextNICIndex(instanceID)
	out := *a

	return &out, nil
}

// DetachVNIC detaches a secondary VNIC and deletes it. The primary VNIC only
// goes away with the instance, as it does in real OCI.
func (m *Mock) DetachVNIC(ctx context.Context, attachmentID string) error {
	vnicID, err := m.removeVNICAttachment(attachmentID)
	if err != nil {
		return err
	}

	unplace(ctx, m.networking(), vnicID)

	return nil
}

func (m *Mock) removeVNICAttachment(attachmentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.vnicAttach.Get(attachmentID)
	if !ok {
		return "", notFoundf("VNIC attachment %q not found", attachmentID)
	}

	d, ok := m.details.Get(a.InstanceID)
	if ok && d.VNICID == a.VNICID {
		return "", cerrors.Newf(cerrors.FailedPrecondition,
			"VNIC attachment %q is primary; it is detached with the instance", attachmentID)
	}

	m.vnicAttach.Delete(attachmentID)
	m.forget(attachmentID)

	return a.VNICID, nil
}

// nextNICIndex is the position a new VNIC takes on an instance. The caller
// holds m.mu.
func (m *Mock) nextNICIndex(instanceID string) int {
	n := 0

	for _, a := range m.vnicAttach.All() {
		if a.InstanceID == instanceID {
			n++
		}
	}

	return n - 1
}

// instanceAD returns an instance's availability domain, or a not-found error.
func (m *Mock) instanceAD(instanceID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return "", instanceNotFound(instanceID)
	}

	return inst.AD, nil
}

func bootVolumeNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "boot volume %q not found", id)
}

// bootVolumeName is what OCI calls an instance's boot volume when the caller
// named the instance.
func bootVolumeName(instanceName string) string {
	if instanceName == "" {
		return ""
	}

	return instanceName + " (Boot Volume)"
}
