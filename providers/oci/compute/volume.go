package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
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

// OCI's volume performance levels run from 0 to 120 VPUs per GB in steps of 10.
const (
	maxVpusPerGB = 120
	vpusStep     = 10
)

// iSCSI target details OCI hands a guest: the portal sits on a link-local
// address on port 3260.
const (
	iscsiPort      = 3260
	iscsiFirstHost = 2
	iscsiHostRange = 253
)

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

// Block volume size bounds OCI enforces, in GBs.
const (
	minVolumeSizeGBs = 50
	maxVolumeSizeGBs = 32768
)

// sourceSizeLocked returns the size of the resource a clone or restore copies,
// or 0 for a volume created empty. The caller holds m.mu.
func (m *Mock) sourceSizeLocked(source SourceDetails) int {
	switch source.SourceType {
	case sourceVolume:
		if v, ok := m.volumes.Get(source.ID); ok {
			return v.Size
		}
	case sourceVolumeBackup:
		if b, ok := m.backups.Get(source.ID); ok {
			return b.Size
		}
	case sourceBootVolume:
		if b, ok := m.bootVolumes.Get(source.ID); ok {
			return b.SizeInGBs
		}
	}

	return 0
}

// volumeSize resolves a create's size: a clone or restore without one takes
// its source's, and none may be smaller than its source or outside OCI's
// bounds. The caller holds m.mu.
func (m *Mock) volumeSize(requested int, source SourceDetails) (int, error) {
	sourceSize := m.sourceSizeLocked(source)

	size := requested
	if size == 0 {
		size = sourceSize
	}

	switch {
	case size <= 0:
		return 0, cerrors.New(cerrors.InvalidArgument, "volume size in GBs is required")
	case size < sourceSize:
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"volume size %d GBs is smaller than its %d GBs source", size, sourceSize)
	case size < minVolumeSizeGBs || size > maxVolumeSizeGBs:
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"volume size %d GBs is outside %d-%d GBs", size, minVolumeSizeGBs, maxVolumeSizeGBs)
	}

	return size, nil
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
	size, err := m.volumeSize(cfg.Size, source)
	if err != nil {
		return nil, err
	}

	cfg.Size = size

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

	if !m.volumes.Has(id) {
		return volumeNotFound(id)
	}

	if atts := m.attachmentsOf(id); len(atts) > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"volume %q is still attached to %q", id, atts[0].InstanceID)
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

	if v.AD != "" && inst.AD != "" && v.AD != inst.AD {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"volume %q in %s cannot attach to instance %q in %s", spec.VolumeID, v.AD, spec.InstanceID, inst.AD)
	}

	if err := sharingConflict(spec, m.attachmentsOf(spec.VolumeID)); err != nil {
		return nil, err
	}

	attachmentType, err := resolveAttachmentType(spec.AttachmentType)
	if err != nil {
		return nil, err
	}

	spec.AttachmentType = attachmentType

	id := m.newOCID(typeVolumeAttachment)
	att := spec
	att.ID = id
	att.AvailabilityDomain = inst.AD
	att.LifecycleState = StateAttached
	att.TimeCreated = m.now()

	if att.AttachmentType == AttachmentISCSI {
		m.setISCSITarget(&att)
	}

	m.volAttach.Set(id, &att)
	m.record(id)

	m.refreshVolumeAttachment(spec.VolumeID)

	out := att

	return &out, nil
}

// DetachVolume detaches a block volume. instanceID and device narrow the
// detach to the attachments matching them when given, as the portable driver
// allows; OCI addresses an attachment by its OCID, which DetachVolumeAttachment
// serves.
func (m *Mock) DetachVolume(_ context.Context, volumeID, instanceID, device string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.volumes.Has(volumeID) {
		return volumeNotFound(volumeID)
	}

	atts := m.attachmentsOf(volumeID)
	if len(atts) == 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "volume %q is not attached", volumeID)
	}

	matched := 0

	for _, a := range atts {
		if (instanceID != "" && instanceID != a.InstanceID) || (device != "" && device != a.Device) {
			continue
		}

		m.volAttach.Delete(a.ID)
		m.forget(a.ID)

		matched++
	}

	if matched == 0 {
		return notFoundf("volume %q is not attached to instance %q as device %q", volumeID, instanceID, device)
	}

	m.refreshVolumeAttachment(volumeID)

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
	m.refreshVolumeAttachment(a.VolumeID)

	return nil
}

// resolveAttachmentType applies OCI's default attachment type and rejects the
// ones CloudEmu does not model.
func resolveAttachmentType(t string) (string, error) {
	switch t {
	case "", AttachmentServiceDetermined:
		return AttachmentParavirtualized, nil
	case AttachmentParavirtualized, AttachmentISCSI:
		return t, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "unsupported volume attachment type %q", t)
	}
}

// setISCSITarget gives an iSCSI attachment the target a guest logs in to.
func (m *Mock) setISCSITarget(att *VolumeAttachment) {
	n := m.iscsiCounter.Add(1)
	att.IPv4 = "169.254.2." + itoa(n%iscsiHostRange+iscsiFirstHost)
	att.IQN = "iqn.2015-12.com.oracleiaas:" + idgen.UUID()
	att.Port = iscsiPort
}

// attachmentsOf returns a volume's live attachments, ordered by OCID. The
// caller holds m.mu.
func (m *Mock) attachmentsOf(volumeID string) []*VolumeAttachment {
	out := make([]*VolumeAttachment, 0)

	for _, a := range m.volAttach.SortedValues() {
		if a.VolumeID == volumeID {
			out = append(out, a)
		}
	}

	return out
}

// sharingConflict reports why a volume cannot take another attachment: OCI
// lets a volume attach to several instances only when every attachment,
// including the new one, is shareable, and never twice to one instance.
//
//nolint:gocritic // hugeParam: VolumeAttachment is the requested attachment.
func sharingConflict(spec VolumeAttachment, existing []*VolumeAttachment) error {
	for _, a := range existing {
		switch {
		case a.InstanceID == spec.InstanceID:
			return cerrors.Newf(cerrors.AlreadyExists,
				"volume %q is already attached to %q", spec.VolumeID, a.InstanceID)
		case !spec.IsShareable || !a.IsShareable:
			return cerrors.Newf(cerrors.AlreadyExists,
				"volume %q is already attached to %q; every attachment of a shared volume must be shareable",
				spec.VolumeID, a.InstanceID)
		}
	}

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

// UpdateBlockVolume changes a block volume's size, performance and tags. Shrinking
// is refused, as OCI refuses it.
func (m *Mock) UpdateBlockVolume(_ context.Context, id string, upd Update, sizeInGBs, vpusPerGB int) (
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

	if sizeInGBs > maxVolumeSizeGBs {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"volume size %d GBs is outside %d-%d GBs", sizeInGBs, minVolumeSizeGBs, maxVolumeSizeGBs)
	}

	if !validVpusPerGB(vpusPerGB) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"vpusPerGB %d must be 0, 10, 20 or 30 to 120 in steps of 10", vpusPerGB)
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
			v.Tags = replaceTags(v.Tags, upd.Tags)
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

// refreshVolumeAttachment derives a volume's portable attachment fields from
// the attachments it has left: a shared volume stays in use until its last
// one goes. The caller holds m.mu.
func (m *Mock) refreshVolumeAttachment(volumeID string) {
	atts := m.attachmentsOf(volumeID)

	m.volumes.Update(volumeID, func(v *volumeData) *volumeData {
		if len(atts) == 0 {
			v.AttachedTo, v.Device, v.State = "", "", volumeAvailable

			return v
		}

		v.AttachedTo, v.Device, v.State = atts[0].InstanceID, atts[0].Device, volumeInUse

		return v
	})
}

// validVpusPerGB reports whether a performance level is one OCI offers: 0,
// 10, 20, or 30 to 120 in steps of 10. Zero on an update leaves it alone.
func validVpusPerGB(vpus int) bool {
	return vpus >= 0 && vpus <= maxVpusPerGB && vpus%vpusStep == 0
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
