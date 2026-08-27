package loadbalancer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// lbSnapshot is the full serialized state of the Azure Load Balancer mock. The
// five stores hold fully-exported driver value types and round-trip through the
// generic memstore helper; the health and attrs maps are string-keyed all the
// way down, so they serialize directly. The mutexes and *config.Options are
// intentionally not captured.
type lbSnapshot struct {
	LBs       json.RawMessage                            `json:"lbs,omitempty"`
	TargetGrp json.RawMessage                            `json:"targetGroups,omitempty"`
	Listeners json.RawMessage                            `json:"listeners,omitempty"`
	Rules     json.RawMessage                            `json:"rules,omitempty"`
	AzureLBs  json.RawMessage                            `json:"azureLbs,omitempty"`
	Health    map[string]map[string]*driver.TargetHealth `json:"health,omitempty"`
	Attrs     map[string]driver.LBAttributes             `json:"attrs,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the Azure LB mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap lbSnapshot

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.snapshotMaps(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *lbSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.LBs, m.lbs.Snapshot},
		{&snap.TargetGrp, m.tgs.Snapshot},
		{&snap.Listeners, m.listeners.Snapshot},
		{&snap.Rules, m.rules.Snapshot},
		{&snap.AzureLBs, m.azureLBs.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("azure loadbalancer: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func (m *Mock) snapshotMaps(snap *lbSnapshot) {
	m.healthMu.RLock()
	if len(m.health) > 0 {
		snap.Health = make(map[string]map[string]*driver.TargetHealth, len(m.health))

		for arn, tg := range m.health {
			inner := make(map[string]*driver.TargetHealth, len(tg))
			for id, h := range tg {
				inner[id] = h
			}

			snap.Health[arn] = inner
		}
	}
	m.healthMu.RUnlock()

	m.attrsMu.RLock()
	if len(m.attrs) > 0 {
		snap.Attrs = make(map[string]driver.LBAttributes, len(m.attrs))
		for k, v := range m.attrs {
			snap.Attrs[k] = v
		}
	}
	m.attrsMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every ARN and
// target id round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap lbSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure loadbalancer: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.restoreMaps(&snap)

	return nil
}

func (m *Mock) restoreStores(snap *lbSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.LBs, m.lbs.LoadSnapshot},
		{snap.TargetGrp, m.tgs.LoadSnapshot},
		{snap.Listeners, m.listeners.LoadSnapshot},
		{snap.Rules, m.rules.LoadSnapshot},
		{snap.AzureLBs, m.azureLBs.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("azure loadbalancer: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreMaps(snap *lbSnapshot) {
	if len(snap.Health) > 0 {
		m.healthMu.Lock()
		for arn, tg := range snap.Health {
			m.health[arn] = tg
		}
		m.healthMu.Unlock()
	}

	if len(snap.Attrs) > 0 {
		m.attrsMu.Lock()
		for k, v := range snap.Attrs {
			m.attrs[k] = v
		}
		m.attrsMu.Unlock()
	}
}
