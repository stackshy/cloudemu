package eventgrid

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// egSnapshot is the full serialized state of the Event Grid mock: the custom
// event buses (topics) and the separate system-topic delivery buses, each keyed
// by name with its rules, per-rule targets, and buffered events. busData and
// ruleData are built from unexported fields and nested stores, so they are
// promoted to exported snapshot forms; the target store's value type
// (driver.Target) is fully exported and round-trips through the generic memstore
// helper. A rule's parsed filter/destination are NOT serialized: they are
// deterministically re-derived from the rule's raw ARM properties (rule.Description)
// on restore, exactly as PutRule derives them. The mutexes, the wired HTTP
// client / Service Bus / Functions deliverers, the monitoring backend, and
// *config.Options are intentionally not captured.
type egSnapshot struct {
	Buses       map[string]*busSnapshot `json:"buses,omitempty"`
	SystemBuses map[string]*busSnapshot `json:"systemBuses,omitempty"`
}

// busSnapshot mirrors busData, promoting its nested rule store and its buffered
// event log.
type busSnapshot struct {
	Info   driver.EventBusInfo      `json:"info"`
	Rules  map[string]*ruleSnapshot `json:"rules,omitempty"`
	Events []driver.Event           `json:"events,omitempty"`
}

// ruleSnapshot mirrors ruleData, promoting its nested target store. The parsed
// filter/dest are omitted (re-derived from Rule.Description on restore).
type ruleSnapshot struct {
	Rule    driver.Rule     `json:"rule"`
	Targets json.RawMessage `json:"targets,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Event Grid holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	buses, err := snapshotBuses(m.buses)
	if err != nil {
		return nil, err
	}

	systemBuses, err := snapshotBuses(m.systemBuses)
	if err != nil {
		return nil, err
	}

	return json.Marshal(egSnapshot{Buses: buses, SystemBuses: systemBuses})
}

func snapshotBuses(store *memstore.Store[*busData]) (map[string]*busSnapshot, error) {
	if store.Len() == 0 {
		return nil, nil
	}

	out := make(map[string]*busSnapshot, store.Len())

	for name, bd := range store.All() {
		bs, err := snapshotBus(bd)
		if err != nil {
			return nil, err
		}

		out[name] = bs
	}

	return out, nil
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
				return nil, fmt.Errorf("eventgrid: snapshot targets: %w", err)
			}

			bs.Rules[name] = &ruleSnapshot{Rule: rd.rule, Targets: targets}
		}
	}

	return bs, nil
}

// Restore rebuilds every custom and system bus under its original name (rules
// under their names, targets under their ids); each rule's filter/destination is
// re-derived from its raw ARM properties so PutEvents delivery behaves as before.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap egSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("eventgrid: parse snapshot: %w", err)
	}

	if err := restoreBuses(m.buses, snap.Buses); err != nil {
		return err
	}

	return restoreBuses(m.systemBuses, snap.SystemBuses)
}

func restoreBuses(store *memstore.Store[*busData], buses map[string]*busSnapshot) error {
	for name, bs := range buses {
		bd, err := restoreBus(bs)
		if err != nil {
			return err
		}

		store.Set(name, bd)
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
		rd := &ruleData{
			rule:    rs.Rule,
			targets: memstore.New[driver.Target](),
			filter:  parseSubscriptionFilter(rs.Rule.Description),
			dest:    parseSubscriptionDestination(rs.Rule.Description),
		}

		if len(rs.Targets) > 0 {
			if err := rd.targets.LoadSnapshot(rs.Targets); err != nil {
				return nil, fmt.Errorf("eventgrid: restore targets: %w", err)
			}
		}

		bd.rules.Set(name, rd)
	}

	return bd, nil
}
