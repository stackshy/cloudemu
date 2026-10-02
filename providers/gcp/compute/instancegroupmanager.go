package compute

import (
	"encoding/json"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// InstanceGroupManager is the in-memory record backing a zonal or regional GCE
// managed instance group (compute#instanceGroupManager). A regional group sets
// Region and leaves Zone empty. Only the fields the emulator
// round-trips are modeled: targetSize is the load-bearing one, since the
// Terraform google provider derives a GKE node pool's node_count by summing the
// targetSize of the MIGs its instanceGroupUrls point at. Host-dependent links
// (selfLink, zone URL, instanceGroup URL) are built by the wire handler from the
// request host, so they are not stored here. Spec keeps the insert body so a
// read echoes the fields the emulator does not model (versions, policies).
type InstanceGroupManager struct {
	Name             string          `json:"name"`
	Zone             string          `json:"zone"`
	Region           string          `json:"region,omitempty"`
	Spec             json.RawMessage `json:"spec,omitempty"`
	TargetSize       int             `json:"targetSize"`
	BaseInstanceName string          `json:"baseInstanceName,omitempty"`
	InstanceTemplate string          `json:"instanceTemplate,omitempty"`
	CreatedAt        string          `json:"createdAt,omitempty"`
}

// migKey scopes a managed instance group by its zone or region, since MIG
// names are unique per scope (the same name in two zones is two groups). Zone
// and region names never collide (us-central1-a vs us-central1).
func migKey(scope, name string) string {
	return scope + "/" + name
}

// Scope returns the zone of a zonal group or the region of a regional one.
func (igm *InstanceGroupManager) Scope() string {
	if igm.Zone != "" {
		return igm.Zone
	}

	return igm.Region
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

	if igm.Scope() == "" {
		return cerrors.New(cerrors.InvalidArgument, "instance group manager zone or region is required")
	}

	if igm.CreatedAt == "" {
		igm.CreatedAt = m.opts.Clock.Now().UTC().Format(timeFormat)
	}

	igm.Spec = append(json.RawMessage(nil), igm.Spec...)

	if !m.migs.SetIfAbsent(migKey(igm.Scope(), igm.Name), igm) {
		return cerrors.Newf(cerrors.AlreadyExists, "instance group manager %q already exists in %q", igm.Name, igm.Scope())
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

// GetInstanceGroupManagerGCP returns a MIG by zone (or region) and name.
func (m *Mock) GetInstanceGroupManagerGCP(scope, name string) (InstanceGroupManager, bool) {
	igm, ok := m.migs.Get(migKey(scope, name))
	if ok {
		igm.Spec = append(json.RawMessage(nil), igm.Spec...)
	}

	return igm, ok
}

// ListInstanceGroupManagersGCP returns every MIG in the given zone or region.
func (m *Mock) ListInstanceGroupManagersGCP(scope string) []InstanceGroupManager {
	all := m.migs.All()
	out := make([]InstanceGroupManager, 0, len(all))

	for _, igm := range all {
		if igm.Scope() == scope {
			igm.Spec = append(json.RawMessage(nil), igm.Spec...)
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

// PatchInstanceGroupManagerGCP applies an update (instanceGroupManagers.patch)
// to the group's targetSize, base name, template and stored spec. Returns
// NotFound when the group does not exist.
//
//nolint:gocritic // hugeParam: value struct is the natural record shape here.
func (m *Mock) PatchInstanceGroupManagerGCP(scope, name string, patched InstanceGroupManager) error {
	if patched.TargetSize < 0 {
		return cerrors.New(cerrors.InvalidArgument, "targetSize must be >= 0")
	}

	updated := m.migs.Update(migKey(scope, name), func(igm InstanceGroupManager) InstanceGroupManager {
		igm.TargetSize = patched.TargetSize
		igm.BaseInstanceName = patched.BaseInstanceName
		igm.InstanceTemplate = patched.InstanceTemplate
		igm.Spec = append(json.RawMessage(nil), patched.Spec...)

		return igm
	})

	if !updated {
		return cerrors.Newf(cerrors.NotFound, "instance group manager %q not found in %q", name, scope)
	}

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
