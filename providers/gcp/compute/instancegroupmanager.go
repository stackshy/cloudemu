package compute

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// InstanceGroupManager is the in-memory record backing a zonal GCE managed
// instance group (compute#instanceGroupManager). Only the fields the emulator
// round-trips are modeled — targetSize is the load-bearing one, since the
// Terraform google provider derives a GKE node pool's node_count by summing the
// targetSize of the MIGs its instanceGroupUrls point at. Host-dependent links
// (selfLink, zone URL, instanceGroup URL) are built by the wire handler from the
// request host, so they are not stored here.
type InstanceGroupManager struct {
	Name             string `json:"name"`
	Zone             string `json:"zone"`
	TargetSize       int    `json:"targetSize"`
	BaseInstanceName string `json:"baseInstanceName,omitempty"`
	InstanceTemplate string `json:"instanceTemplate,omitempty"`
	CreatedAt        string `json:"createdAt,omitempty"`
}

// migKey scopes a managed instance group by zone, since MIG names are unique
// per-zone (the same name in two zones is two distinct groups).
func migKey(zone, name string) string {
	return zone + "/" + name
}

// CreateInstanceGroupManagerGCP registers a zonal MIG, rejecting a duplicate
// name in the same zone. Used by the compute wire handler's insert route
// (google_compute_instance_group_manager / instanceGroupManagers.insert).
//
//nolint:gocritic // hugeParam: value struct is the natural record shape here.
func (m *Mock) CreateInstanceGroupManagerGCP(igm InstanceGroupManager) error {
	if igm.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "instance group manager name is required")
	}

	if igm.Zone == "" {
		return cerrors.New(cerrors.InvalidArgument, "instance group manager zone is required")
	}

	if igm.CreatedAt == "" {
		igm.CreatedAt = m.opts.Clock.Now().UTC().Format(timeFormat)
	}

	if !m.migs.SetIfAbsent(migKey(igm.Zone, igm.Name), igm) {
		return cerrors.Newf(cerrors.AlreadyExists, "instance group manager %q already exists in zone %q", igm.Name, igm.Zone)
	}

	return nil
}

// UpsertInstanceGroupManagerGCP creates or overwrites a MIG, preserving the
// original creation timestamp on an update. It is the idempotent write GKE uses
// to keep a node pool's backing MIG targetSize in sync with the pool's node
// count, so a repeated reconcile (cluster create, pool create, pool resize)
// never errors on an already-present group.
//
//nolint:gocritic // hugeParam: value struct is the natural record shape here.
func (m *Mock) UpsertInstanceGroupManagerGCP(igm InstanceGroupManager) {
	if igm.Name == "" || igm.Zone == "" {
		return
	}

	if existing, ok := m.migs.Get(migKey(igm.Zone, igm.Name)); ok && existing.CreatedAt != "" {
		igm.CreatedAt = existing.CreatedAt
	}

	if igm.CreatedAt == "" {
		igm.CreatedAt = m.opts.Clock.Now().UTC().Format(timeFormat)
	}

	m.migs.Set(migKey(igm.Zone, igm.Name), igm)
}

// GetInstanceGroupManagerGCP returns a MIG by zone and name.
func (m *Mock) GetInstanceGroupManagerGCP(zone, name string) (InstanceGroupManager, bool) {
	return m.migs.Get(migKey(zone, name))
}

// ListInstanceGroupManagersGCP returns every MIG in the given zone.
func (m *Mock) ListInstanceGroupManagersGCP(zone string) []InstanceGroupManager {
	all := m.migs.All()
	out := make([]InstanceGroupManager, 0, len(all))

	for _, igm := range all {
		if igm.Zone == zone {
			out = append(out, igm)
		}
	}

	return out
}

// AllInstanceGroupManagersGCP returns every MIG across all zones, for
// aggregatedList.
func (m *Mock) AllInstanceGroupManagersGCP() []InstanceGroupManager {
	all := m.migs.All()
	out := make([]InstanceGroupManager, 0, len(all))

	for _, igm := range all {
		out = append(out, igm)
	}

	return out
}

// DeleteInstanceGroupManagerGCP removes a MIG. A missing group is a no-op for
// GKE cleanup callers, but the wire delete route checks existence first and
// returns 404 itself, so this never needs to surface NotFound.
func (m *Mock) DeleteInstanceGroupManagerGCP(zone, name string) error {
	m.migs.Delete(migKey(zone, name))
	return nil
}

// ResizeInstanceGroupManagerGCP sets a MIG's targetSize (instanceGroupManagers.
// resize / setTargetSize). Returns NotFound when the group does not exist.
func (m *Mock) ResizeInstanceGroupManagerGCP(zone, name string, size int) error {
	if size < 0 {
		return cerrors.New(cerrors.InvalidArgument, "targetSize must be >= 0")
	}

	updated := m.migs.Update(migKey(zone, name), func(igm InstanceGroupManager) InstanceGroupManager {
		igm.TargetSize = size
		return igm
	})

	if !updated {
		return cerrors.Newf(cerrors.NotFound, "instance group manager %q not found in zone %q", name, zone)
	}

	return nil
}
