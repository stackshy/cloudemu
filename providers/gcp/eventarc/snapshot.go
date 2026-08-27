package eventarc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// eventarcSnapshot is the full serialized state of the Eventarc mock: every
// channel (event bus) keyed by name, each with its triggers (rules), per-rule
// targets, and buffered events. busData and ruleData are built from unexported
// fields and nested stores, so they are promoted to exported snapshot forms; the
// target store's value type (driver.Target) is fully exported and round-trips
// through the generic memstore helper. The mutexes, the wired Functions /
// Cloud Run deliverers, the HTTP client, the monitoring backend, and
// *config.Options are intentionally not captured.
type eventarcSnapshot struct {
	Buses map[string]*busSnapshot `json:"buses,omitempty"`
}

// busSnapshot mirrors busData, promoting its nested rule store and its buffered
// event log.
type busSnapshot struct {
	Info   driver.EventBusInfo      `json:"info"`
	Rules  map[string]*ruleSnapshot `json:"rules,omitempty"`
	Events []driver.Event           `json:"events,omitempty"`
}

// ruleSnapshot mirrors ruleData, promoting its nested target store.
type ruleSnapshot struct {
	Rule    driver.Rule     `json:"rule"`
	Targets json.RawMessage `json:"targets,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Eventarc holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := eventarcSnapshot{Buses: make(map[string]*busSnapshot, m.buses.Len())}

	for name, bd := range m.buses.All() {
		bs, err := snapshotBus(bd)
		if err != nil {
			return nil, err
		}

		snap.Buses[name] = bs
	}

	if len(snap.Buses) == 0 {
		snap.Buses = nil
	}

	return json.Marshal(snap)
}

func snapshotBus(bd *busData) (*busSnapshot, error) {
	bd.mu.RLock()
	defer bd.mu.RUnlock()

	bs := &busSnapshot{Info: bd.info}

	if len(bd.events) > 0 {
		bs.Events = make([]driver.Event, len(bd.events))
		copy(bs.Events, bd.events)
	}

	if bd.rules.Len() > 0 {
		bs.Rules = make(map[string]*ruleSnapshot, bd.rules.Len())

		for name, rd := range bd.rules.All() {
			targets, err := rd.targets.Snapshot()
			if err != nil {
				return nil, fmt.Errorf("eventarc: snapshot targets: %w", err)
			}

			bs.Rules[name] = &ruleSnapshot{Rule: rd.rule, Targets: targets}
		}
	}

	return bs, nil
}

// Restore rebuilds every channel under its original name (rules under their
// names, targets under their ids).
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap eventarcSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("eventarc: parse snapshot: %w", err)
	}

	for name, bs := range snap.Buses {
		bd, err := restoreBus(bs)
		if err != nil {
			return err
		}

		m.buses.Set(name, bd)
	}

	return nil
}

func restoreBus(bs *busSnapshot) (*busData, error) {
	bd := &busData{
		info:  bs.Info,
		rules: memstore.New[*ruleData](),
	}

	if len(bs.Events) > 0 {
		bd.events = make([]driver.Event, len(bs.Events))
		copy(bd.events, bs.Events)
	}

	for name, rs := range bs.Rules {
		rd := &ruleData{rule: rs.Rule, targets: memstore.New[driver.Target]()}
		if len(rs.Targets) > 0 {
			if err := rd.targets.LoadSnapshot(rs.Targets); err != nil {
				return nil, fmt.Errorf("eventarc: restore targets: %w", err)
			}
		}

		bd.rules.Set(name, rd)
	}

	return bd, nil
}
