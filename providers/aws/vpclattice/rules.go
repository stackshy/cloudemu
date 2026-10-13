package vpclattice

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func ruleNotFound(id string) error {
	return errors.Newf(errors.NotFound, "rule %q not found", id)
}

const defaultRuleName = "default"

// defaultRuleID is the stable id of a listener's synthesized default rule.
func defaultRuleID(listenerID string) string {
	return "rule-" + strings.TrimPrefix(listenerID, "listener-")
}

// defaultRule builds the default rule every listener has in real VPC Lattice.
// It is derived from the listener on read and never stored.
func (m *Mock) defaultRule(l *driver.Listener) driver.Rule {
	id := defaultRuleID(l.ID)

	return driver.Rule{
		ID:            id,
		ARN:           m.arn("service/" + l.ServiceID + "/listener/" + l.ID + "/rule/" + id),
		Name:          defaultRuleName,
		ServiceID:     l.ServiceID,
		ListenerID:    l.ID,
		IsDefault:     true,
		Action:        append([]byte(nil), l.DefaultAction...),
		CreatedAt:     l.CreatedAt,
		LastUpdatedAt: l.LastUpdatedAt,
	}
}

// defaultRuleListener returns the listener when ruleID names its default rule.
// Caller holds m.mu.
func (m *Mock) defaultRuleListener(serviceID, listenerID, ruleID string) (*driver.Listener, bool) {
	l, err := m.getListenerLocked(serviceID, listenerID)
	if err != nil || idFromIdentifier(ruleID) != defaultRuleID(l.ID) {
		return nil, false
	}

	return l, true
}

// errDefaultRuleImmutable is returned for UpdateRule / BatchUpdateRule on a
// listener's default rule, which only UpdateListener may change.
func errDefaultRuleImmutable() error {
	return invalidf("a default listener rule cannot be modified; use UpdateListener")
}

// failureCodeFor maps an error to the per-item failure code BatchUpdateRule reports.
func failureCodeFor(err error) string {
	switch {
	case errors.IsNotFound(err):
		return "ResourceNotFoundException"
	case errors.IsAlreadyExists(err), errors.IsFailedPrecondition(err):
		return "ConflictException"
	case errors.IsInvalidArgument(err):
		return "ValidationException"
	default:
		return "InternalServerException"
	}
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

	if perr := validateRulePriority(in.Priority); perr != nil {
		return nil, perr
	}

	if m.rulePriorityTaken(l.ID, in.Priority, "") {
		return nil, conflictf("priority %d is already used by another rule on listener %q", in.Priority, l.ID)
	}

	id, err := idgen.GenerateLongID("rule-")
	if err != nil {
		return nil, err
	}

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

	if l, ok := m.defaultRuleListener(serviceID, listenerID, ruleID); ok {
		out := m.defaultRule(l)

		return &out, nil
	}

	rule, err := m.getRuleLocked(serviceID, listenerID, ruleID)
	if err != nil {
		return nil, err
	}

	out := cloneRule(rule)

	return &out, nil
}

// applyRuleUpdate mutates a stored rule with non-zero update fields after
// validating the new priority. Nothing changes when it returns an error. Caller
// holds m.mu.
func (m *Mock) applyRuleUpdate(rule *driver.Rule, priority int32, match, action []byte) error {
	if priority != 0 {
		if err := validateRulePriority(priority); err != nil {
			return err
		}

		if m.rulePriorityTaken(rule.ListenerID, priority, rule.ID) {
			return conflictf("priority %d is already used by another rule on listener %q", priority, rule.ListenerID)
		}

		rule.Priority = priority
	}

	if len(match) > 0 {
		rule.Match = append([]byte(nil), match...)
	}

	if len(action) > 0 {
		rule.Action = append([]byte(nil), action...)
	}

	rule.LastUpdatedAt = m.now()

	return nil
}

func (m *Mock) UpdateRule(
	_ context.Context, serviceID, listenerID, ruleID string, priority int32, match, action []byte,
) (*driver.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.defaultRuleListener(serviceID, listenerID, ruleID); ok {
		return nil, errDefaultRuleImmutable()
	}

	rule, err := m.getRuleLocked(serviceID, listenerID, ruleID)
	if err != nil {
		return nil, err
	}

	if uerr := m.applyRuleUpdate(rule, priority, match, action); uerr != nil {
		return nil, uerr
	}

	out := cloneRule(rule)

	return &out, nil
}

func (m *Mock) DeleteRule(_ context.Context, serviceID, listenerID, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.defaultRuleListener(serviceID, listenerID, ruleID); ok {
		return invalidf("the default rule cannot be deleted")
	}

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

	l, err := m.getListenerLocked(sid, lid)
	if err != nil {
		return nil, err
	}

	all := sortedValues(m.rules.All(), cloneRule)

	custom := make([]driver.Rule, 0, len(all))

	for i := range all {
		if all[i].ServiceID == l.ServiceID && all[i].ListenerID == l.ID {
			custom = append(custom, all[i])
		}
	}

	sort.SliceStable(custom, func(i, j int) bool { return custom[i].Priority < custom[j].Priority })

	return append([]driver.Rule{m.defaultRule(l)}, custom...), nil
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

		updated, err := m.batchUpdateOne(serviceID, listenerID, u)
		if err != nil {
			fail = append(fail, driver.RuleUpdateFailure{
				RuleID: u.RuleID, FailureCode: failureCodeFor(err), FailureMessage: errors.Message(err),
			})

			continue
		}

		ok = append(ok, *updated)
	}

	return ok, fail, nil
}

// batchUpdateOne applies one BatchUpdateRule entry. Caller holds m.mu.
func (m *Mock) batchUpdateOne(serviceID, listenerID string, u driver.RuleUpdate) (*driver.Rule, error) {
	if _, isDefault := m.defaultRuleListener(serviceID, listenerID, u.RuleID); isDefault {
		return nil, errDefaultRuleImmutable()
	}

	rule, err := m.getRuleLocked(serviceID, listenerID, u.RuleID)
	if err != nil {
		return nil, err
	}

	if uerr := m.applyRuleUpdate(rule, u.Priority, u.Match, u.Action); uerr != nil {
		return nil, uerr
	}

	out := cloneRule(rule)

	return &out, nil
}
