package eventbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ebSnapshot is the full serialized state of the EventBridge mock: every event
// bus keyed by name (each with its rules, per-rule targets, buffered events, and
// resource-policy statements) plus the per-ARN tag store. busData and ruleData
// are built from unexported fields and nested stores, so they are promoted to
// exported snapshot forms; the target store's value type (driver.Target) is
// fully exported and round-trips through the generic memstore helper. The
// mutexes, the wired SQS/Lambda/SNS/StepFunctions targets, the monitoring
// backend, and *config.Options are intentionally not captured.
type ebSnapshot struct {
	Buses map[string]*busSnapshot      `json:"buses,omitempty"`
	Tags  map[string]map[string]string `json:"tags,omitempty"`
}

// busSnapshot mirrors busData, promoting its nested rule store, its buffered
// event log, and its resource-policy statements.
type busSnapshot struct {
	Info        driver.EventBusInfo      `json:"info"`
	Rules       map[string]*ruleSnapshot `json:"rules,omitempty"`
	Events      []driver.Event           `json:"events,omitempty"`
	PolicyStmts []map[string]any         `json:"policyStmts,omitempty"`
}

// ruleSnapshot mirrors ruleData, promoting its nested target store.
type ruleSnapshot struct {
	Rule    driver.Rule     `json:"rule"`
	Targets json.RawMessage `json:"targets,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// EventBridge holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := ebSnapshot{Buses: make(map[string]*busSnapshot, m.buses.Len())}

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

	snap.Tags = m.snapshotTags()

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

	if len(bd.policyStmts) > 0 {
		bs.PolicyStmts = make([]map[string]any, len(bd.policyStmts))
		copy(bs.PolicyStmts, bd.policyStmts)
	}

	if bd.rules.Len() > 0 {
		bs.Rules = make(map[string]*ruleSnapshot, bd.rules.Len())

		for name, rd := range bd.rules.All() {
			targets, err := rd.targets.Snapshot()
			if err != nil {
				return nil, fmt.Errorf("eventbridge: snapshot targets: %w", err)
			}

			bs.Rules[name] = &ruleSnapshot{Rule: rd.rule, Targets: targets}
		}
	}

	return bs, nil
}

func (m *Mock) snapshotTags() map[string]map[string]string {
	m.tagsByARN.mu.RLock()
	defer m.tagsByARN.mu.RUnlock()

	if len(m.tagsByARN.tags) == 0 {
		return nil
	}

	out := make(map[string]map[string]string, len(m.tagsByARN.tags))

	for arn, tags := range m.tagsByARN.tags {
		inner := make(map[string]string, len(tags))
		for k, v := range tags {
			inner[k] = v
		}

		out[arn] = inner
	}

	return out
}

// Restore rebuilds every event bus under its original name (rules under their
// names, targets under their ids) plus the per-ARN tag store.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap ebSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("eventbridge: parse snapshot: %w", err)
	}

	for name, bs := range snap.Buses {
		bd, err := restoreBus(bs)
		if err != nil {
			return err
		}

		m.buses.Set(name, bd)
	}

	m.restoreTags(snap.Tags)

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

	if len(bs.PolicyStmts) > 0 {
		bd.policyStmts = make([]map[string]any, len(bs.PolicyStmts))
		copy(bd.policyStmts, bs.PolicyStmts)
	}

	for name, rs := range bs.Rules {
		rd := &ruleData{rule: rs.Rule, targets: memstore.New[driver.Target]()}
		if len(rs.Targets) > 0 {
			if err := rd.targets.LoadSnapshot(rs.Targets); err != nil {
				return nil, fmt.Errorf("eventbridge: restore targets: %w", err)
			}
		}

		bd.rules.Set(name, rd)
	}

	return bd, nil
}

func (m *Mock) restoreTags(tags map[string]map[string]string) {
	if len(tags) == 0 {
		return
	}

	m.tagsByARN.mu.Lock()
	defer m.tagsByARN.mu.Unlock()

	if m.tagsByARN.tags == nil {
		m.tagsByARN.tags = make(map[string]map[string]string, len(tags))
	}

	for arn, t := range tags {
		m.tagsByARN.tags[arn] = t
	}
}
