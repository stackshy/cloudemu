package vcn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vcnSnapshot is the full serialized state of the OCI VCN mock. Every memstore
// store is dumped keyed by its resource OCID so cross-references (a subnet's
// VCNID, a route-table association's SubnetID, a peering's requester/accepter
// VCNs) still resolve after a restore. The scopes and created side-stores are
// captured too, so a restored resource keeps its compartment and creation time.
// Every VCN value type is fully exported, so all stores round-trip through the
// generic memstore helper; the mutex and *config.Options are not serialized.
type vcnSnapshot struct {
	VCNs          json.RawMessage `json:"vcns,omitempty"`
	Subnets       json.RawMessage `json:"subnets,omitempty"`
	NSGs          json.RawMessage `json:"nsgs,omitempty"`
	SecurityLists json.RawMessage `json:"securityLists,omitempty"`
	RouteTables   json.RawMessage `json:"routeTables,omitempty"`
	RTAssocs      json.RawMessage `json:"rtAssocs,omitempty"`
	IGWs          json.RawMessage `json:"igws,omitempty"`
	NATGateways   json.RawMessage `json:"natGateways,omitempty"`
	ServiceGWs    json.RawMessage `json:"serviceGateways,omitempty"`
	PublicIPs     json.RawMessage `json:"publicIps,omitempty"`
	VNICs         json.RawMessage `json:"vnics,omitempty"`
	PrivateIPs    json.RawMessage `json:"privateIps,omitempty"`
	DHCPOptions   json.RawMessage `json:"dhcpOptions,omitempty"`
	Peerings      json.RawMessage `json:"peerings,omitempty"`
	LPGs          json.RawMessage `json:"lpgs,omitempty"`
	FlowLogs      json.RawMessage `json:"flowLogs,omitempty"`
	Scopes        json.RawMessage `json:"scopes,omitempty"`
	Created       json.RawMessage `json:"created,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// VCN holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap vcnSnapshot

	for _, d := range m.snapshotDumps(&snap) {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("vcn: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every OCID
// and the id-string cross-references between resources are preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vcnSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("vcn: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.snapshotDumps(&snap) {
		if len(*d.dst) == 0 {
			continue
		}

		if err := d.load(*d.dst); err != nil {
			return fmt.Errorf("vcn: restore store: %w", err)
		}
	}

	return nil
}

// storeDump pairs a snapshot field with its store's dump and load functions, so
// Snapshot and Restore share one table and cannot drift apart.
type storeDump struct {
	dst  *json.RawMessage
	fn   func() ([]byte, error)
	load func([]byte) error
}

// snapshotDumps lists every store alongside the snapshot field it maps to.
func (m *Mock) snapshotDumps(snap *vcnSnapshot) []storeDump {
	return []storeDump{
		{&snap.VCNs, m.vcns.Snapshot, m.vcns.LoadSnapshot},
		{&snap.Subnets, m.subnets.Snapshot, m.subnets.LoadSnapshot},
		{&snap.NSGs, m.nsgs.Snapshot, m.nsgs.LoadSnapshot},
		{&snap.SecurityLists, m.securityLists.Snapshot, m.securityLists.LoadSnapshot},
		{&snap.RouteTables, m.routeTables.Snapshot, m.routeTables.LoadSnapshot},
		{&snap.RTAssocs, m.rtAssocs.Snapshot, m.rtAssocs.LoadSnapshot},
		{&snap.IGWs, m.igws.Snapshot, m.igws.LoadSnapshot},
		{&snap.NATGateways, m.natGateways.Snapshot, m.natGateways.LoadSnapshot},
		{&snap.ServiceGWs, m.serviceGWs.Snapshot, m.serviceGWs.LoadSnapshot},
		{&snap.PublicIPs, m.publicIPs.Snapshot, m.publicIPs.LoadSnapshot},
		{&snap.VNICs, m.vnics.Snapshot, m.vnics.LoadSnapshot},
		{&snap.PrivateIPs, m.privateIPs.Snapshot, m.privateIPs.LoadSnapshot},
		{&snap.DHCPOptions, m.dhcpOptions.Snapshot, m.dhcpOptions.LoadSnapshot},
		{&snap.Peerings, m.peerings.Snapshot, m.peerings.LoadSnapshot},
		{&snap.LPGs, m.lpgs.Snapshot, m.lpgs.LoadSnapshot},
		{&snap.FlowLogs, m.flowLogs.Snapshot, m.flowLogs.LoadSnapshot},
		{&snap.Scopes, m.scopes.Snapshot, m.scopes.LoadSnapshot},
		{&snap.Created, m.created.Snapshot, m.created.LoadSnapshot},
	}
}
