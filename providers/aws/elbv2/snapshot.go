package elbv2

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// elbSnapshot is the full serialized state of the ELBv2 mock. The four core
// stores hold fully-exported driver value types and round-trip through the
// generic memstore helper; the health map is promoted to an exported,
// deterministically-ordered form because its key type (targetKey) is unexported
// and a struct — neither of which json can serialize as a map key. The
// provisioning->active settle overlay, the wired subnet resolver, the mutexes,
// and *config.Options are intentionally not serialized: a restored load balancer
// reports its stored (final) state immediately.
type elbSnapshot struct {
	LBs       json.RawMessage                   `json:"lbs,omitempty"`
	TargetGrp json.RawMessage                   `json:"targetGroups,omitempty"`
	Listeners json.RawMessage                   `json:"listeners,omitempty"`
	Rules     json.RawMessage                   `json:"rules,omitempty"`
	Health    map[string][]targetHealthSnapshot `json:"health,omitempty"`
	Attrs     map[string]driver.LBAttributes    `json:"attrs,omitempty"`
	TGAttrs   map[string]map[string]string      `json:"tgAttrs,omitempty"`
}

// targetHealthSnapshot promotes one (targetKey -> *TargetHealth) entry to an
// exported form so it survives JSON.
type targetHealthSnapshot struct {
	ID     string               `json:"id"`
	Port   int                  `json:"port,omitempty"`
	Health *driver.TargetHealth `json:"health,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// ELBv2 holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap elbSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	snap.Health = m.snapshotHealth()
	m.snapshotAttrs(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *elbSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.LBs, m.lbs.Snapshot},
		{&snap.TargetGrp, m.tgs.Snapshot},
		{&snap.Listeners, m.listeners.Snapshot},
		{&snap.Rules, m.rules.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("elbv2: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// snapshotHealth promotes the nested health map to an exported, key-sorted form.
// The inner slice is sorted by (id, port) so a snapshot->restore->snapshot
// round-trip is byte-stable despite Go's random map iteration order.
func (m *Mock) snapshotHealth() map[string][]targetHealthSnapshot {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()

	if len(m.health) == 0 {
		return nil
	}

	out := make(map[string][]targetHealthSnapshot, len(m.health))

	for arn, tgHealth := range m.health {
		entries := make([]targetHealthSnapshot, 0, len(tgHealth))
		for k, h := range tgHealth {
			entries = append(entries, targetHealthSnapshot{ID: k.id, Port: k.port, Health: h})
		}

		sort.Slice(entries, func(i, j int) bool {
			if entries[i].ID != entries[j].ID {
				return entries[i].ID < entries[j].ID
			}

			return entries[i].Port < entries[j].Port
		})

		out[arn] = entries
	}

	return out
}

func (m *Mock) snapshotAttrs(snap *elbSnapshot) {
	m.attrsMu.RLock()
	if len(m.attrs) > 0 {
		snap.Attrs = make(map[string]driver.LBAttributes, len(m.attrs))
		for k, v := range m.attrs {
			snap.Attrs[k] = v
		}
	}
	m.attrsMu.RUnlock()

	m.tgAttrsMu.RLock()
	if len(m.tgAttrs) > 0 {
		snap.TGAttrs = make(map[string]map[string]string, len(m.tgAttrs))
		for k, v := range m.tgAttrs {
			snap.TGAttrs[k] = v
		}
	}
	m.tgAttrsMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every ARN and
// target id round-trips unchanged, so listeners/rules and registered-target
// health still resolve against their load balancers and target groups.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap elbSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("elbv2: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreHealth(snap.Health)
	m.restoreAttrs(&snap)

	return nil
}

func (m *Mock) restoreStores(snap *elbSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.LBs, m.lbs.LoadSnapshot},
		{snap.TargetGrp, m.tgs.LoadSnapshot},
		{snap.Listeners, m.listeners.LoadSnapshot},
		{snap.Rules, m.rules.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("elbv2: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreHealth(health map[string][]targetHealthSnapshot) {
	if len(health) == 0 {
		return
	}

	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	for arn, entries := range health {
		tgHealth := make(map[targetKey]*driver.TargetHealth, len(entries))
		for _, e := range entries {
			tgHealth[targetKey{id: e.ID, port: e.Port}] = e.Health
		}

		m.health[arn] = tgHealth
	}
}

func (m *Mock) restoreAttrs(snap *elbSnapshot) {
	if len(snap.Attrs) > 0 {
		m.attrsMu.Lock()
		for k, v := range snap.Attrs {
			m.attrs[k] = v
		}
		m.attrsMu.Unlock()
	}

	if len(snap.TGAttrs) > 0 {
		m.tgAttrsMu.Lock()
		for k, v := range snap.TGAttrs {
			m.tgAttrs[k] = v
		}
		m.tgAttrsMu.Unlock()
	}
}
