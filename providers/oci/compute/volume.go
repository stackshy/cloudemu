package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Portable volume states, as the driver's VolumeInfo carries them.
const (
	volumeAvailable = "available"
	volumeInUse     = "in-use"
)

// Source types a volume, boot volume or volume group can be created from.
const (
	sourceImage            = "image"
	sourceVolume           = "volume"
	sourceVolumeBackup     = "volumeBackup"
	sourceBootVolume       = "bootVolume"
	sourceBootVolumeBackup = "bootVolumeBackup"
	sourceVolumeGroup      = "volumeGroup"
)

// Attachment types OCI offers. Paravirtualized is the default; iSCSI carries
// the connection details a guest mounts with.
const (
	AttachmentParavirtualized   = "paravirtualized"
	AttachmentISCSI             = "iscsi"
	AttachmentServiceDetermined = "service_determined"
)

// defaultVpusPerGB is OCI's Balanced performance tier.
const defaultVpusPerGB = 10

type volumeData struct {
	ID         string
	Size       int
	VolumeType string
	State      string
	AD         string
	AttachedTo string
	Device     string
	CreatedAt  string
	Tags       map[string]string
	VpusPerGB  int
	Throughput int
	Tier       string
	Source     SourceDetails
	GroupID    string
	IsHydrated bool
}

// CreateVolume creates a block volume.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) CreateVolume(_ context.Context, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, err := m.addVolume(cfg, SourceDetails{})
	if err != nil {
		return nil, err
	}

	info := toVolumeInfo(v)

	return &info, nil
}

// CreateVolumeFrom creates a block volume cloned from another volume or
// restored from a backup, which the portable VolumeConfig cannot express.
//
//nolint:gocritic // hugeParam: mirrors CreateVolume.
func (m *Mock) CreateVolumeFrom(
	_ context.Context, cfg driver.VolumeConfig, source SourceDetails,
) (*driver.VolumeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateSource(source); err != nil {
		return nil, err
	}

	v, err := m.addVolume(cfg, source)
	if err != nil {
		return nil, err
	}

	info := toVolumeInfo(v)

	return &info, nil
}

// validateSource checks that a clone or restore names a resource that exists.
func (m *Mock) validateSource(source SourceDetails) error {
	switch source.SourceType {
	case "":
		return nil
	case sourceVolume:
		if !m.volumes.Has(source.ID) {
			return notFoundf("volume %q not found", source.ID)
		}
	case sourceVolumeBackup:
		if !m.backups.Has(source.ID) {
			return notFoundf("volume backup %q not found", source.ID)
		}
	case sourceBootVolume:
		if !m.bootVolumes.Has(source.ID) {
			return notFoundf("boot volume %q not found", source.ID)
		}
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported volume source type %q", source.SourceType)
	}

	return nil
}

// addVolume stores a block volume. The caller holds m.mu.
//
//nolint:gocritic // hugeParam: mirrors CreateVolume.
func (m *Mock) addVolume(cfg driver.VolumeConfig, source SourceDetails) (*volumeData, error) {
	if cfg.Size <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "volume size in GBs is required")
	}

	vpus := cfg.IOPS
	if vpus == 0 {
		vpus = defaultVpusPerGB
	}

	id := m.newOCID(typeVolume)
	v := &volumeData{
		ID:         id,
		Size:       cfg.Size,
		VolumeType: cfg.VolumeType,
		State:      volumeAvailable,
		AD:         orDefault(cfg.AvailabilityZone, m.defaultAD()),
		CreatedAt:  m.now(),
		Tags:       copyTags(cfg.Tags),
		VpusPerGB:  vpus,
		Throughput: cfg.Throughput,
		Tier:       cfg.Tier,
		Source:     source,
		IsHydrated: true,
	}

	m.volumes.Set(id, v)
	m.record(id)

	return v, nil
}

// DeleteVolume deletes a block volume, refusing while it is attached.
func (m *Mock) DeleteVolume(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes.Get(id)
	if !ok {
		return volumeNotFound(id)
	}

	if v.AttachedTo != "" {
		return cerrors.Newf(cerrors.FailedPrecondition, "volume %q is still attached to %q", id, v.AttachedTo)
	}

	for _, g := range m.volGroups.All() {
		if contains(g.VolumeIDs, id) {
			return cerrors.Newf(cerrors.FailedPrecondition, "volume %q is still in volume group %q", id, g.ID)
		}
	}

	m.volumes.Delete(id)
	m.forget(id)

	return nil
}

// DescribeVolumes returns block volumes matching the given OCIDs, or all if
// empty.
func (m *Mock) DescribeVolumes(_ context.Context, ids []string) ([]driver.VolumeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.volumes, ids, toVolumeInfo), nil
}

// AttachVolume attaches a block volume to an instance, recording the OCI
// attachment resource that carries its own OCID.
func (m *Mock) AttachVolume(ctx context.Context, volumeID, instanceID, device string) error {
	_, err := m.AttachVolumeToInstance(ctx, VolumeAttachment{
		InstanceID:     instanceID,
		VolumeID:       volumeID,
		Device:         device,
		AttachmentType: AttachmentParavirtualized,
	})

	return err
}

// AttachVolumeToInstance attaches a block volume and returns OCI's attachment
// resource, which the portable AttachVolume has no room to report.
//
//nolint:gocritic // hugeParam: VolumeAttachment is the value type being stored.
func (m *Mock) AttachVolumeToInstance(_ context.Context, spec VolumeAttachment) (*VolumeAttachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(spec.InstanceID)
	if !ok {
		return nil, instanceNotFound(spec.InstanceID)
	}

	v, ok := m.volumes.Get(spec.VolumeID)
	if !ok {
		return nil, volumeNotFound(spec.VolumeID)
	}

	if v.AttachedTo != "" && !spec.IsShareable {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"volume %q is already attached to %q", spec.VolumeID, v.AttachedTo)
	}

	if spec.AttachmentType == "" || spec.AttachmentType == AttachmentServiceDetermined {
		spec.AttachmentType = AttachmentParavirtualized
	}

	if spec.AttachmentType != AttachmentParavirtualized && spec.AttachmentType != AttachmentISCSI {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported volume attachment type %q", spec.AttachmentType)
	}

	id := m.newOCID(typeVolumeAttachment)
	att := spec
	att.ID = id
	att.AvailabilityDomain = inst.AD
	att.LifecycleState = StateAttached
	att.TimeCreated = m.now()

	m.volAttach.Set(id, &att)
	m.record(id)

	m.volumes.Update(spec.VolumeID, func(v *volumeData) *volumeData {
		v.AttachedTo = spec.InstanceID
		v.Device = spec.Device
		v.State = volumeInUse

		return v
	})

	out := att

	return &out, nil
}

// DetachVolume detaches a block volume from whatever instance holds it.
func (m *Mock) DetachVolume(_ context.Context, volumeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes.Get(volumeID)
	if !ok {
		return volumeNotFound(volumeID)
	}

	if v.AttachedTo == "" {
		return cerrors.Newf(cerrors.FailedPrecondition, "volume %q is not attached", volumeID)
	}

	for _, a := range m.volAttach.All() {
		if a.VolumeID == volumeID {
			m.volAttach.Delete(a.ID)
			m.forget(a.ID)
		}
	}

	m.markVolumeDetached(volumeID)

	return nil
}

// DetachVolumeAttachment detaches by attachment OCID, which is how OCI's
// DetachVolume addresses it.
func (m *Mock) DetachVolumeAttachment(_ context.Context, attachmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.volAttach.Get(attachmentID)
	if !ok {
		return notFoundf("volume attachment %q not found", attachmentID)
	}

	m.volAttach.Delete(attachmentID)
	m.forget(attachmentID)
	m.markVolumeDetached(a.VolumeID)

	return nil
}

// GetVolumeAttachment returns one volume attachment.
func (m *Mock) GetVolumeAttachment(_ context.Context, attachmentID string) (*VolumeAttachment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.volAttach.Get(attachmentID)
	if !ok {
		return nil, notFoundf("volume attachment %q not found", attachmentID)
	}

	out := *a

	return &out, nil
}

// ListVolumeAttachments returns the volume attachments in a compartment,
// narrowed to an instance or a volume when either is named.
func (m *Mock) ListVolumeAttachments(_ context.Context, compartmentID, instanceID, volumeID string) (
	[]VolumeAttachment, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listScoped(m, m.volAttach, compartmentID,
		func(a *VolumeAttachment) string { return a.ID },
		func(a *VolumeAttachment) bool {
			return matchesBoth(instanceID, a.InstanceID, volumeID, a.VolumeID)
		}), nil
}

// UpdateVolume changes a block volume's size, performance and tags. Shrinking
// is refused, as OCI refuses it.
func (m *Mock) UpdateVolume(_ context.Context, id string, upd Update, sizeInGBs, vpusPerGB int) (
	*driver.VolumeInfo, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes.Get(id)
	if !ok {
		return nil, volumeNotFound(id)
	}

	if sizeInGBs != 0 && sizeInGBs < v.Size {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"volume %q cannot shrink from %d to %d GBs", id, v.Size, sizeInGBs)
	}

	m.volumes.Update(id, func(v *volumeData) *volumeData {
		if sizeInGBs != 0 {
			v.Size = sizeInGBs
		}

		if vpusPerGB != 0 {
			v.VpusPerGB = vpusPerGB
		}

		if upd.DisplayName != nil {
			v.Tags = withTag(v.Tags, TagDisplayName, *upd.DisplayName)
		}

		if upd.Tags != nil {
			v.Tags = mergeTags(v.Tags, upd.Tags)
		}

		return v
	})

	updated, _ := m.volumes.Get(id)
	info := toVolumeInfo(updated)

	return &info, nil
}

// VolumeSource returns what a block volume was cloned or restored from.
func (m *Mock) VolumeSource(id string) (source SourceDetails, vpusPerGB int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.volumes.Get(id)
	if !ok {
		return SourceDetails{}, 0
	}

	return v.Source, v.VpusPerGB
}

// markVolumeDetached clears a volume's attachment. The caller holds m.mu.
func (m *Mock) markVolumeDetached(volumeID string) {
	m.volumes.Update(volumeID, func(v *volumeData) *volumeData {
		v.AttachedTo = ""
		v.Device = ""
		v.State = volumeAvailable

		return v
	})
}

func toVolumeInfo(v *volumeData) driver.VolumeInfo {
	return driver.VolumeInfo{
		ID:               v.ID,
		Size:             v.Size,
		VolumeType:       v.VolumeType,
		State:            v.State,
		AvailabilityZone: v.AD,
		AttachedTo:       v.AttachedTo,
		Device:           v.Device,
		CreatedAt:        v.CreatedAt,
		Tags:             copyTags(v.Tags),
		IOPS:             v.VpusPerGB,
		Throughput:       v.Throughput,
		Tier:             v.Tier,
	}
}

func volumeNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "volume %q not found", id)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

// mergeTags returns a fresh map containing existing's keys plus tags's keys,
// with tags winning on overlap.
func mergeTags(existing, tags map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(tags))

	for k, v := range existing {
		out[k] = v
	}

	for k, v := range tags {
		out[k] = v
	}

	return out
}
