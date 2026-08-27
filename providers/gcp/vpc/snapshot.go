package vpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vpcSnapshot is the full serialized state of the GCP VPC mock. Every memstore
// store is dumped keyed by its resource id (a GCP self-link) so cross-references
// — a subnet's VPCID, a GCE instance's SubnetID/network refs held in the compute
// mock — still resolve after a restore. Every stored value type is fully
// exported, so all stores round-trip through the generic memstore helper. The
// wired *config.Options is intentionally not serialized.
type vpcSnapshot struct {
	VPCs           json.RawMessage `json:"vpcs,omitempty"`
	Subnets        json.RawMessage `json:"subnets,omitempty"`
	SecurityGroups json.RawMessage `json:"securityGroups,omitempty"`
	Peerings       json.RawMessage `json:"peerings,omitempty"`
	NATGateways    json.RawMessage `json:"natGateways,omitempty"`
	FlowLogs       json.RawMessage `json:"flowLogs,omitempty"`
	RouteTables    json.RawMessage `json:"routeTables,omitempty"`
	NetworkACLs    json.RawMessage `json:"networkAcls,omitempty"`
	IGWs           json.RawMessage `json:"igws,omitempty"`
	EIPs           json.RawMessage `json:"eips,omitempty"`
	RTAssocs       json.RawMessage `json:"rtAssocs,omitempty"`
	Endpoints      json.RawMessage `json:"endpoints,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// VPC holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap vpcSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *vpcSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.VPCs, m.vpcs.Snapshot},
		{&snap.Subnets, m.subnets.Snapshot},
		{&snap.SecurityGroups, m.securityGroups.Snapshot},
		{&snap.Peerings, m.peerings.Snapshot},
		{&snap.NATGateways, m.natGateways.Snapshot},
		{&snap.FlowLogs, m.flowLogs.Snapshot},
		{&snap.RouteTables, m.routeTables.Snapshot},
		{&snap.NetworkACLs, m.networkACLs.Snapshot},
		{&snap.IGWs, m.igws.Snapshot},
		{&snap.EIPs, m.eips.Snapshot},
		{&snap.RTAssocs, m.rtAssocs.Snapshot},
		{&snap.Endpoints, m.endpoints.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("gcp vpc: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// resource id (and the id-string cross-references a GCE instance holds — subnet
// self-links, network refs) is preserved, so a restored instance's networking
// refs still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vpcSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("gcp vpc: parse snapshot: %w", err)
	}

	return m.restoreStores(&snap)
}

func (m *Mock) restoreStores(snap *vpcSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.VPCs, m.vpcs.LoadSnapshot},
		{snap.Subnets, m.subnets.LoadSnapshot},
		{snap.SecurityGroups, m.securityGroups.LoadSnapshot},
		{snap.Peerings, m.peerings.LoadSnapshot},
		{snap.NATGateways, m.natGateways.LoadSnapshot},
		{snap.FlowLogs, m.flowLogs.LoadSnapshot},
		{snap.RouteTables, m.routeTables.LoadSnapshot},
		{snap.NetworkACLs, m.networkACLs.LoadSnapshot},
		{snap.IGWs, m.igws.LoadSnapshot},
		{snap.EIPs, m.eips.LoadSnapshot},
		{snap.RTAssocs, m.rtAssocs.LoadSnapshot},
		{snap.Endpoints, m.endpoints.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("gcp vpc: restore store: %w", err)
		}
	}

	return nil
}
