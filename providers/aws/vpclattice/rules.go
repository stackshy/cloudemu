package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func ruleNotFound(id string) error {
	return errors.Newf(errors.NotFound, "rule %q not found", id)
}

func cloneRule(r *driver.Rule) driver.Rule {
	out := *r
	out.Match = append([]byte(nil), r.Match...)
	out.Action = append([]byte(nil), r.Action...)

	return out
}

func (m *Mock) CreateRule(_ context.Context, in *driver.CreateRuleInput) (*driver.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.getListenerLocked(in.ServiceID, in.ListenerID)
	if err != nil {
		return nil, err
	}

	id := idgen.GenerateID("rule-")
	rule := &driver.Rule{
		ID:            id,
		ARN:           m.arn("service/" + l.ServiceID + "/listener/" + l.ID + "/rule/" + id),
		Name:          in.Name,
		ServiceID:     l.ServiceID,
		ListenerID:    l.ID,
		Priority:      in.Priority,
		Match:         append([]byte(nil), in.Match...),
		Action:        append([]byte(nil), in.Action...),
		CreatedAt:     m.now(),
		LastUpdatedAt: m.now(),
	}
	m.rules.Set(id, rule)
	m.writeTags(rule.ARN, in.Tags)

	out := cloneRule(rule)

	return &out, nil
}

// getRuleLocked resolves a rule scoped to a listener+service. Caller holds m.mu.
func (m *Mock) getRuleLocked(serviceID, listenerID, ruleID string) (*driver.Rule, error) {
	sid := idFromIdentifier(serviceID)
	lid := idFromIdentifier(listenerID)
	rid := idFromIdentifier(ruleID)

	rule, ok := m.rules.Get(rid)
	if !ok || rule.ServiceID != sid || rule.ListenerID != lid {
		return nil, ruleNotFound(rid)
	}

	return rule, nil
}

func (m *Mock) GetRule(_ context.Context, serviceID, listenerID, ruleID string) (*driver.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, err := m.getRuleLocked(serviceID, listenerID, ruleID)
	if err != nil {
		return nil, err
	}

	out := cloneRule(rule)

	return &out, nil
}

// applyRuleUpdate mutates a stored rule with non-zero update fields. Caller
// holds m.mu.
func (m *Mock) applyRuleUpdate(rule *driver.Rule, priority int32, match, action []byte) {
	if priority != 0 {
		rule.Priority = priority
	}

	if len(match) > 0 {
		rule.Match = append([]byte(nil), match...)
	}

	if len(action) > 0 {
		rule.Action = append([]byte(nil), action...)
	}

	rule.LastUpdatedAt = m.now()
}

func (m *Mock) UpdateRule(
	_ context.Context, serviceID, listenerID, ruleID string, priority int32, match, action []byte,
) (*driver.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, err := m.getRuleLocked(serviceID, listenerID, ruleID)
	if err != nil {
		return nil, err
	}

	m.applyRuleUpdate(rule, priority, match, action)

	out := cloneRule(rule)

	return &out, nil
}

func (m *Mock) DeleteRule(_ context.Context, serviceID, listenerID, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, err := m.getRuleLocked(serviceID, listenerID, ruleID)
	if err != nil {
		return err
	}

	m.rules.Delete(rule.ID)

	return nil
}

func (m *Mock) ListRules(_ context.Context, serviceID, listenerID string) ([]driver.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sid := idFromIdentifier(serviceID)
	lid := idFromIdentifier(listenerID)

	all := sortedValues(m.rules.All(), cloneRule)

	out := make([]driver.Rule, 0, len(all))

	for i := range all {
		if all[i].ServiceID == sid && all[i].ListenerID == lid {
			out = append(out, all[i])
		}
	}

	return out, nil
}

func (m *Mock) BatchUpdateRules(
	_ context.Context, serviceID, listenerID string, updates []driver.RuleUpdate,
) ([]driver.Rule, []driver.RuleUpdateFailure, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ok := make([]driver.Rule, 0, len(updates))
	fail := make([]driver.RuleUpdateFailure, 0)

	for i := range updates {
		u := updates[i]

		rule, err := m.getRuleLocked(serviceID, listenerID, u.RuleID)
		if err != nil {
			fail = append(fail, driver.RuleUpdateFailure{
				RuleID: u.RuleID, FailureCode: "ResourceNotFoundException", FailureMessage: err.Error(),
			})

			continue
		}

		m.applyRuleUpdate(rule, u.Priority, u.Match, u.Action)
		ok = append(ok, cloneRule(rule))
	}

	return ok, fail, nil
}
