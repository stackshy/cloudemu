package vpclattice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vpclatticeSnapshot is the full serialized state of the AWS VPC Lattice mock.
// Every memstore store holds a fully-exported value type — a *driver pointer type
// for each resource, or a plain []driver.RegisteredTarget / string /
// map[string]string for the targets, resource-policy, and tag stores — so each
// round-trips through the generic memstore helper under its exact key. The keys
// are the resource ids and ARNs (targets keyed by target-group id, resource
// policies and tags by resource ARN), so the id/ARN cross-references records hold
// survive a restore. The wired opts and the serializing mutex are intentionally
// not serialized.
type vpclatticeSnapshot struct {
	ServiceNetworks json.RawMessage `json:"serviceNetworks,omitempty"`
	Services        json.RawMessage `json:"services,omitempty"`
	Listeners       json.RawMessage `json:"listeners,omitempty"`
	Rules           json.RawMessage `json:"rules,omitempty"`
	TargetGroups    json.RawMessage `json:"targetGroups,omitempty"`
	Targets         json.RawMessage `json:"targets,omitempty"`
	SNVpcAssocs     json.RawMessage `json:"snVpcAssocs,omitempty"`
	SNSvcAssocs     json.RawMessage `json:"snSvcAssocs,omitempty"`
	SNResAssocs     json.RawMessage `json:"snResAssocs,omitempty"`
	ResourceConfigs json.RawMessage `json:"resourceConfigs,omitempty"`
	ResourceGws     json.RawMessage `json:"resourceGws,omitempty"`
	AccessLogSubs   json.RawMessage `json:"accessLogSubs,omitempty"`
	AuthPolicies    json.RawMessage `json:"authPolicies,omitempty"`
	ResourcePolics  json.RawMessage `json:"resourcePolics,omitempty"`
	DomainVerifs    json.RawMessage `json:"domainVerifs,omitempty"`
	Tags            json.RawMessage `json:"tags,omitempty"`
}

// storeRefs pairs each snapshot field with its store's Snapshot/LoadSnapshot
// entry points, so dump and load walk the identical, symmetric list.
func (m *Mock) storeRefs(snap *vpclatticeSnapshot) []struct {
	dst  *json.RawMessage
	dump func() ([]byte, error)
	load func([]byte) error
} {
	return []struct {
		dst  *json.RawMessage
		dump func() ([]byte, error)
		load func([]byte) error
	}{
		{&snap.ServiceNetworks, m.serviceNetworks.Snapshot, m.serviceNetworks.LoadSnapshot},
		{&snap.Services, m.services.Snapshot, m.services.LoadSnapshot},
		{&snap.Listeners, m.listeners.Snapshot, m.listeners.LoadSnapshot},
		{&snap.Rules, m.rules.Snapshot, m.rules.LoadSnapshot},
		{&snap.TargetGroups, m.targetGroups.Snapshot, m.targetGroups.LoadSnapshot},
		{&snap.Targets, m.targets.Snapshot, m.targets.LoadSnapshot},
		{&snap.SNVpcAssocs, m.snVpcAssocs.Snapshot, m.snVpcAssocs.LoadSnapshot},
		{&snap.SNSvcAssocs, m.snSvcAssocs.Snapshot, m.snSvcAssocs.LoadSnapshot},
		{&snap.SNResAssocs, m.snResAssocs.Snapshot, m.snResAssocs.LoadSnapshot},
		{&snap.ResourceConfigs, m.resourceConfigs.Snapshot, m.resourceConfigs.LoadSnapshot},
		{&snap.ResourceGws, m.resourceGws.Snapshot, m.resourceGws.LoadSnapshot},
		{&snap.AccessLogSubs, m.accessLogSubs.Snapshot, m.accessLogSubs.LoadSnapshot},
		{&snap.AuthPolicies, m.authPolicies.Snapshot, m.authPolicies.LoadSnapshot},
		{&snap.ResourcePolics, m.resourcePolics.Snapshot, m.resourcePolics.LoadSnapshot},
		{&snap.DomainVerifs, m.domainVerifs.Snapshot, m.domainVerifs.LoadSnapshot},
		{&snap.Tags, m.tags.Snapshot, m.tags.LoadSnapshot},
	}
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// VPC Lattice is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var snap vpclatticeSnapshot

	for _, r := range m.storeRefs(&snap) {
		b, err := r.dump()
		if err != nil {
			return nil, fmt.Errorf("vpclattice: snapshot store: %w", err)
		}

		*r.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every resource
// id, ARN, and association id (and the id/ARN cross-references records hold) is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vpclatticeSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("vpclattice: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.storeRefs(&snap) {
		if len(*r.dst) == 0 {
			continue
		}

		if err := r.load(*r.dst); err != nil {
			return fmt.Errorf("vpclattice: restore store: %w", err)
		}
	}

	return nil
}
