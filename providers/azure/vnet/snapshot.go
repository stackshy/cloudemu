package vnet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vnetSnapshot is the full serialized state of the Azure VNet mock. Every
// memstore store is dumped keyed by its resource id (or ARM addressing key for
// NICs) so cross-references — a subnet's VPCID, a VM's NIC/subnet refs held in
// the VirtualMachines mock — still resolve after a restore. Every stored value
// type is fully exported, so all stores round-trip through the generic memstore
// helper. The mutexes and the wired *config.Options are intentionally not
// serialized.
type vnetSnapshot struct {
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
	NICs           json.RawMessage `json:"nics,omitempty"`

	AzureVNetMeta       json.RawMessage `json:"azureVnetMeta,omitempty"`
	AzureNSGMeta        json.RawMessage `json:"azureNsgMeta,omitempty"`
	AzureRouteTableMeta json.RawMessage `json:"azureRouteTableMeta,omitempty"`
	AzureVNetPeerings   json.RawMessage `json:"azureVnetPeerings,omitempty"`
	AzureASGs           json.RawMessage `json:"azureAsgs,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// VNet holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap vnetSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *vnetSnapshot) error {
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
		{&snap.NICs, m.nics.Snapshot},
		{&snap.AzureVNetMeta, m.azureVNetMeta.Snapshot},
		{&snap.AzureNSGMeta, m.azureNSGMeta.Snapshot},
		{&snap.AzureRouteTableMeta, m.azureRouteTableMeta.Snapshot},
		{&snap.AzureVNetPeerings, m.azureVNetPeerings.Snapshot},
		{&snap.AzureASGs, m.azureASGs.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("vnet: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// resource id (and the id-string cross-references a VM holds — NIC ids, subnet
// ids) is preserved, so a restored VM's networking refs still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vnetSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("vnet: parse snapshot: %w", err)
	}

	return m.restoreStores(&snap)
}

func (m *Mock) restoreStores(snap *vnetSnapshot) error {
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
		{snap.NICs, m.nics.LoadSnapshot},
		{snap.AzureVNetMeta, m.azureVNetMeta.LoadSnapshot},
		{snap.AzureNSGMeta, m.azureNSGMeta.LoadSnapshot},
		{snap.AzureRouteTableMeta, m.azureRouteTableMeta.LoadSnapshot},
		{snap.AzureVNetPeerings, m.azureVNetPeerings.LoadSnapshot},
		{snap.AzureASGs, m.azureASGs.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("vnet: restore store: %w", err)
		}
	}

	return nil
}
