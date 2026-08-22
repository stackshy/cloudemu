package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// CreateVolumeGroup creates a consistency group over existing block and boot
// volumes, or clones one from another group.
//
//nolint:gocritic // hugeParam: VolumeGroup is the value type being stored.
func (m *Mock) CreateVolumeGroup(_ context.Context, spec VolumeGroup) (*VolumeGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	members, size, err := m.resolveGroupMembers(spec)
	if err != nil {
		return nil, err
	}

	id := m.newOCID(typeVolumeGroup)
	g := spec
	g.ID = id
	g.VolumeIDs = members
	g.SizeInGBs = size
	g.LifecycleState = StateAvailable
	g.TimeCreated = m.now()
	g.Tags = copyTags(spec.Tags)

	if g.AvailabilityDomain == "" {
		g.AvailabilityDomain = m.defaultAD()
	}

	m.volGroups.Set(id, &g)
	m.record(id)

	for _, member := range members {
		m.volumes.Update(member, func(v *volumeData) *volumeData {
			v.GroupID = id

			return v
		})
	}

	out := g

	return &out, nil
}

// resolveGroupMembers validates a group's source and returns its member
// volumes and total size. The caller holds m.mu.
//
//nolint:gocritic // hugeParam: mirrors CreateVolumeGroup.
func (m *Mock) resolveGroupMembers(spec VolumeGroup) ([]string, int, error) {
	members := spec.VolumeIDs

	switch spec.SourceType {
	case "", sourceVolume:
	case sourceVolumeGroup:
		src, ok := m.volGroups.Get(spec.SourceID)
		if !ok {
			return nil, 0, notFoundf("volume group %q not found", spec.SourceID)
		}

		members = src.VolumeIDs
	default:
		return nil, 0, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported volume group source type %q", spec.SourceType)
	}

	if len(members) == 0 {
		return nil, 0, cerrors.New(cerrors.InvalidArgument, "a volume group needs at least one volume")
	}

	size := 0
	out := make([]string, 0, len(members))

	for _, member := range members {
		switch {
		case m.volumes.Has(member):
			v, _ := m.volumes.Get(member)
			size += v.Size
		case m.bootVolumes.Has(member):
			bv, _ := m.bootVolumes.Get(member)
			size += bv.SizeInGBs
		default:
			return nil, 0, notFoundf("volume %q not found", member)
		}

		out = append(out, member)
	}

	return out, size, nil
}

// GetVolumeGroup returns one volume group.
func (m *Mock) GetVolumeGroup(_ context.Context, id string) (*VolumeGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.volGroups.Get(id)
	if !ok {
		return nil, volumeGroupNotFound(id)
	}

	out := *g
	out.VolumeIDs = copyStrings(g.VolumeIDs)

	return &out, nil
}

// ListVolumeGroups returns the volume groups in a compartment.
func (m *Mock) ListVolumeGroups(_ context.Context, compartmentID string) ([]VolumeGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]VolumeGroup, 0)

	for _, g := range m.volGroups.SortedValues() {
		if s, _ := m.scopes.Get(g.ID); s.Compartment != compartmentID {
			continue
		}

		item := *g
		item.VolumeIDs = copyStrings(g.VolumeIDs)
		out = append(out, item)
	}

	return out, nil
}

// UpdateVolumeGroup changes a volume group's display name, membership and tags.
func (m *Mock) UpdateVolumeGroup(_ context.Context, id string, upd Update, volumeIDs []string) (*VolumeGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.volGroups.Has(id) {
		return nil, volumeGroupNotFound(id)
	}

	size := 0

	if volumeIDs != nil {
		members, total, err := m.resolveGroupMembers(VolumeGroup{VolumeIDs: volumeIDs})
		if err != nil {
			return nil, err
		}

		volumeIDs, size = members, total
	}

	m.volGroups.Update(id, func(g *VolumeGroup) *VolumeGroup {
		if upd.DisplayName != nil {
			g.DisplayName = *upd.DisplayName
		}

		if upd.Tags != nil {
			g.Tags = mergeTags(g.Tags, upd.Tags)
		}

		if volumeIDs != nil {
			g.VolumeIDs = copyStrings(volumeIDs)
			g.SizeInGBs = size
		}

		return g
	})

	updated, _ := m.volGroups.Get(id)
	out := *updated
	out.VolumeIDs = copyStrings(updated.VolumeIDs)

	return &out, nil
}

// DeleteVolumeGroup deletes a volume group. Its member volumes survive, as
// they do in real OCI.
func (m *Mock) DeleteVolumeGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.volGroups.Get(id)
	if !ok {
		return volumeGroupNotFound(id)
	}

	for _, member := range g.VolumeIDs {
		m.volumes.Update(member, func(v *volumeData) *volumeData {
			v.GroupID = ""

			return v
		})
	}

	m.volGroups.Delete(id)
	m.forget(id)

	return nil
}

func volumeGroupNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "volume group %q not found", id)
}
