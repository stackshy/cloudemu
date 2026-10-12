package vpclattice

import (
	"context"
	"sort"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const (
	targetStatusInitial  = "INITIAL"
	targetStatusHealthy  = "HEALTHY"
	targetStatusDraining = "DRAINING"
)

// targetKey uniquely identifies a target within a group by id+port.
func targetKey(t driver.RegisteredTarget) string {
	return t.ID + "|" + strconv.Itoa(int(t.Port))
}

// targetSettleKey identifies a target's health window.
func targetSettleKey(tgID string, t driver.RegisteredTarget) string {
	return tgID + "|" + targetKey(t)
}

func (m *Mock) RegisterTargets(
	_ context.Context, tgID string, in []driver.RegisteredTarget,
) ([]driver.RegisteredTarget, []driver.TargetFailure, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(tgID)

	if !m.targetGroups.Has(id) {
		return nil, nil, targetGroupNotFound(id)
	}

	cur, _ := m.targets.Get(id)
	index := make(map[string]int, len(cur))

	for i := range cur {
		index[targetKey(cur[i])] = i
	}

	ok := make([]driver.RegisteredTarget, 0, len(in))

	for _, t := range in {
		t.Status = targetStatusHealthy

		if pos, exists := index[targetKey(t)]; exists {
			cur[pos] = t
		} else {
			index[targetKey(t)] = len(cur)
			cur = append(cur, t)

			m.statusSettle.Begin(targetSettleKey(id, t), targetStatusInitial, m.opts.Clock.Now(),
				m.opts.SettleDuration(settle.DefaultTargetHealthSettle))
		}

		ok = append(ok, t)
	}

	m.targets.Set(id, cur)

	return ok, nil, nil
}

func (m *Mock) DeregisterTargets(
	_ context.Context, tgID string, in []driver.RegisteredTarget,
) ([]driver.RegisteredTarget, []driver.TargetFailure, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(tgID)

	if !m.targetGroups.Has(id) {
		return nil, nil, targetGroupNotFound(id)
	}

	drop := make(map[string]struct{}, len(in))
	for _, t := range in {
		drop[targetKey(t)] = struct{}{}

		m.statusSettle.Clear(targetSettleKey(id, t))
	}

	cur, _ := m.targets.Get(id)

	kept := make([]driver.RegisteredTarget, 0, len(cur))

	for i := range cur {
		if _, gone := drop[targetKey(cur[i])]; !gone {
			kept = append(kept, cur[i])
		}
	}

	m.targets.Set(id, kept)

	ok := make([]driver.RegisteredTarget, 0, len(in))

	for _, t := range in {
		t.Status = targetStatusDraining
		ok = append(ok, t)
	}

	return ok, nil, nil
}

func (m *Mock) ListTargets(_ context.Context, tgID string) ([]driver.RegisteredTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(tgID)

	if !m.targetGroups.Has(id) {
		return nil, targetGroupNotFound(id)
	}

	cur, _ := m.targets.Get(id)

	out := append([]driver.RegisteredTarget(nil), cur...)
	for i := range out {
		out[i].Status = m.statusSettle.State(targetSettleKey(id, out[i]), m.opts.Clock.Now(), out[i].Status)
	}

	sort.Slice(out, func(i, j int) bool {
		return targetKey(out[i]) < targetKey(out[j])
	})

	return out, nil
}
