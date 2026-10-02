package cloudwatch

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmrule"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// compositeAlarmData is a stored composite alarm. Its AlarmRule is a boolean
// expression over other alarms' states. The rule is evaluated when a child
// alarm changes state, when the composite is put, and when a suppressor
// period ends.
type compositeAlarmData struct {
	Name                    string
	ARN                     string
	AlarmRule               string
	AlarmDescription        string
	State                   string
	StateReason             string
	StateReasonData         string
	StateUpdatedTimestamp   time.Time
	ActionsEnabled          bool
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	Tags                    map[string]string
	// StateTransitionedTimestamp is when State last changed.
	StateTransitionedTimestamp time.Time
	// ConfigUpdatedAt is when the configuration was last put.
	ConfigUpdatedAt time.Time
	// ActionsSuppressor names the alarm whose ALARM state holds back actions.
	// WaitPeriod and ExtensionPeriod are in seconds.
	ActionsSuppressor string
	WaitPeriod        int
	ExtensionPeriod   int
	// ActionsSuppressedBy is WaitPeriod, ExtensionPeriod or Alarm while actions
	// are held back. SuppressUntil is when an active period ends.
	ActionsSuppressedBy     string
	ActionsSuppressedReason string
	SuppressUntil           time.Time
	// PendingActions is set while the actions of a transition are held back.
	// PendingFrom is the state that transition left.
	PendingActions bool
	PendingFrom    string
}

// initialCompositeReason is the reason of a composite that has not been
// evaluated yet.
const initialCompositeReason = "Unchecked: Initial alarm creation"

// PutCompositeAlarm creates or updates a composite alarm. Every alarm the rule
// references must exist, as on AWS. A new alarm starts in INSUFFICIENT_DATA
// and is evaluated at once. An update keeps the state and tags, replaces the
// configuration and is evaluated again. Both publish a configuration change
// event.
//
//nolint:gocritic // hugeParam: matches the optional-capability interface signature.
func (m *Mock) PutCompositeAlarm(ctx context.Context, cfg driver.CompositeAlarmConfig) error {
	rule, err := validateCompositeConfig(&cfg)
	if err != nil {
		return err
	}

	now := m.opts.Clock.Now()
	c := m.newCompositeData(&cfg, now)

	m.alarmMu.Lock()

	if missing := m.missingRefsLocked(rule, cfg.Name); len(missing) > 0 {
		m.alarmMu.Unlock()

		return errors.Newf(errors.FailedPrecondition,
			"Could not save the composite alarm as alarms [%s] in the alarm rule do not exist", strings.Join(missing, ", "))
	}

	existing, update := m.compositeAlarms.Get(cfg.Name)
	if update {
		keepCompositeState(c, existing)
	}

	m.compositeAlarms.Set(cfg.Name, c)

	var config *alarmStateEvent

	trigger := compositeTrigger{created: true}

	if update {
		config = compositeConfigEventLocked(operationUpdate, c, existing)
		trigger = compositeTrigger{updated: true}
	} else {
		config = compositeConfigEventLocked(operationCreate, c, nil)
	}

	var notices []*alarmNotice

	if n := m.evaluateCompositeLocked(c, trigger, now); n != nil {
		notices = append(notices, n)
		notices = append(notices, m.settleLocked([]string{c.Name}, now)...)
	}

	m.alarmMu.Unlock()

	m.emitEvent(ctx, config)
	m.publish(ctx, notices...)

	return nil
}

// validateCompositeConfig checks the request fields that need no stored state
// and returns the parsed rule.
func validateCompositeConfig(cfg *driver.CompositeAlarmConfig) (*alarmrule.Rule, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "AlarmName is required")
	}

	if cfg.AlarmRule == "" || strings.TrimSpace(cfg.AlarmRule) != cfg.AlarmRule {
		return nil, errors.New(errors.InvalidArgument, "AlarmRule must not contain leading or trailing whitespace or be null")
	}

	rule, err := alarmrule.Parse(cfg.AlarmRule)
	if err != nil {
		return nil, errors.New(errors.InvalidArgument, err.Error())
	}

	if cfg.ActionsSuppressor == "" {
		return rule, nil
	}

	if cfg.ActionsSuppressorWaitPeriod == nil || cfg.ActionsSuppressorExtensionPeriod == nil {
		return nil, errors.New(errors.InvalidArgument,
			"ActionsSuppressorWaitPeriod and ActionsSuppressorExtensionPeriod are required when ActionsSuppressor is set")
	}

	if *cfg.ActionsSuppressorWaitPeriod < 0 || *cfg.ActionsSuppressorExtensionPeriod < 0 {
		return nil, errors.New(errors.InvalidArgument, "ActionsSuppressor periods must not be negative")
	}

	return rule, nil
}

// newCompositeData builds a fresh composite record from its config.
func (m *Mock) newCompositeData(cfg *driver.CompositeAlarmConfig, now time.Time) *compositeAlarmData {
	actionsEnabled := true
	if cfg.ActionsEnabled != nil {
		actionsEnabled = *cfg.ActionsEnabled
	}

	c := &compositeAlarmData{
		Name:                       cfg.Name,
		ARN:                        idgen.AWSARN("cloudwatch", m.opts.Region, m.opts.AccountID, "alarm:"+cfg.Name),
		AlarmRule:                  cfg.AlarmRule,
		AlarmDescription:           cfg.AlarmDescription,
		State:                      stateInsufficientData,
		StateReason:                initialCompositeReason,
		StateUpdatedTimestamp:      now,
		StateTransitionedTimestamp: now,
		ConfigUpdatedAt:            now,
		ActionsEnabled:             actionsEnabled,
		AlarmActions:               append([]string{}, cfg.AlarmActions...),
		OKActions:                  append([]string{}, cfg.OKActions...),
		InsufficientDataActions:    append([]string{}, cfg.InsufficientDataActions...),
		Tags:                       copyMap(cfg.Tags),
		ActionsSuppressor:          cfg.ActionsSuppressor,
	}

	if cfg.ActionsSuppressor != "" {
		c.WaitPeriod = *cfg.ActionsSuppressorWaitPeriod
		c.ExtensionPeriod = *cfg.ActionsSuppressorExtensionPeriod
	}

	return c
}

// keepCompositeState carries the state, tags and, while the suppressor is
// unchanged, the suppression of an existing composite over to its update.
// Replacing the suppressor discards any active period, as on AWS.
func keepCompositeState(c, existing *compositeAlarmData) {
	c.State = existing.State
	c.StateReason = existing.StateReason
	c.StateReasonData = existing.StateReasonData
	c.StateUpdatedTimestamp = existing.StateUpdatedTimestamp
	c.StateTransitionedTimestamp = existing.StateTransitionedTimestamp
	c.Tags = existing.Tags

	if c.ActionsSuppressor == existing.ActionsSuppressor {
		c.ActionsSuppressedBy = existing.ActionsSuppressedBy
		c.ActionsSuppressedReason = existing.ActionsSuppressedReason
		c.SuppressUntil = existing.SuppressUntil
		c.PendingActions = existing.PendingActions
		c.PendingFrom = existing.PendingFrom
	}
}

// missingRefsLocked returns the rule references that name no stored alarm.
// A reference to the composite being put counts as existing. The caller
// holds alarmMu.
func (m *Mock) missingRefsLocked(rule *alarmrule.Rule, self string) []string {
	var missing []string

	for _, ref := range rule.Refs() {
		name := alarmNameFromRef(ref)
		if name == self {
			continue
		}

		if _, ok := m.alarmStateLocked(name); !ok {
			missing = append(missing, ref)
		}
	}

	return missing
}

// DescribeCompositeAlarms returns composite alarms matching the given names, or
// all composite alarms when names is empty. Due alarms and ended suppressor
// periods are evaluated first.
func (m *Mock) DescribeCompositeAlarms(ctx context.Context, names []string) ([]driver.CompositeAlarmInfo, error) {
	m.evaluateDue(ctx, m.opts.Clock.Now())

	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	if len(names) == 0 {
		all := m.compositeAlarms.All()
		out := make([]driver.CompositeAlarmInfo, 0, len(all))

		for _, a := range all {
			out = append(out, toCompositeAlarmInfo(a))
		}

		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

		return out, nil
	}

	out := make([]driver.CompositeAlarmInfo, 0, len(names))

	for _, name := range names {
		a, ok := m.compositeAlarms.Get(name)
		if !ok {
			continue
		}

		out = append(out, toCompositeAlarmInfo(a))
	}

	return out, nil
}

// DeleteCompositeAlarms deletes the named composite alarms and skips other
// names. It applies the same rules as DeleteAlarms.
func (m *Mock) DeleteCompositeAlarms(ctx context.Context, names []string) error {
	var composites []string

	for _, name := range names {
		if m.compositeAlarms.Has(name) {
			composites = append(composites, name)
		}
	}

	return m.DeleteAlarms(ctx, composites)
}

// DeleteAlarms deletes metric and composite alarms in one call, as the
// DeleteAlarms API does. Names that match no alarm are skipped. The call is
// refused, and nothing is deleted, when it names more than one composite
// alarm or when a composite that is not being deleted still references one of
// the alarms.
func (m *Mock) DeleteAlarms(ctx context.Context, names []string) error {
	m.alarmMu.Lock()

	events, err := m.deleteAlarmsLocked(names)

	m.alarmMu.Unlock()

	if err != nil {
		return err
	}

	for _, ev := range events {
		m.emitEvent(ctx, ev)
	}

	return nil
}

// deleteAlarmsLocked validates and applies a DeleteAlarms call and returns the
// configuration events to publish. The caller holds alarmMu.
func (m *Mock) deleteAlarmsLocked(names []string) ([]*alarmStateEvent, error) {
	deleting := make(map[string]bool, len(names))
	composites := 0

	for _, name := range names {
		if deleting[name] {
			continue
		}

		if _, ok := m.compositeAlarms.Get(name); ok {
			composites++
		} else if _, ok := m.alarms.Get(name); !ok {
			continue
		}

		deleting[name] = true
	}

	if composites > 1 {
		return nil, errors.New(errors.InvalidArgument, "You can delete at most one composite alarm in a DeleteAlarms call")
	}

	if err := m.checkNotReferencedLocked(names, deleting); err != nil {
		return nil, err
	}

	var events []*alarmStateEvent

	for _, name := range names {
		if !deleting[name] {
			continue
		}

		delete(deleting, name)

		if a, ok := m.alarms.Get(name); ok {
			events = append(events, configEventLocked(operationDelete, a, nil))

			m.alarms.Delete(name)

			continue
		}

		c, _ := m.compositeAlarms.Get(name)
		events = append(events, compositeConfigEventLocked(operationDelete, c, nil))
		m.compositeAlarms.Delete(name)
	}

	return events, nil
}

// checkNotReferencedLocked returns the AWS ValidationError text when a
// composite outside deleting references one of the alarms being deleted.
func (m *Mock) checkNotReferencedLocked(names []string, deleting map[string]bool) error {
	var blocked []string

	seen := map[string]bool{}

	for _, name := range names {
		if !deleting[name] || seen[name] {
			continue
		}

		seen[name] = true

		if m.referencedLocked(name, deleting) {
			blocked = append(blocked, name)
		}
	}

	switch len(blocked) {
	case 0:
		return nil
	case 1:
		return errors.Newf(errors.FailedPrecondition,
			"Cannot delete %s as there are composite alarm(s) depending on it.", blocked[0])
	default:
		return errors.Newf(errors.FailedPrecondition,
			"Cannot delete %s as there are composite alarm(s) depending on them.", strings.Join(blocked, ","))
	}
}

// referencedLocked reports whether a composite that is not in skip has name in
// its rule. The caller holds alarmMu.
func (m *Mock) referencedLocked(name string, skip map[string]bool) bool {
	for _, c := range m.compositeAlarms.All() {
		if skip[c.Name] {
			continue
		}

		for _, child := range compositeChildren(c) {
			if child == name {
				return true
			}
		}
	}

	return false
}

func toCompositeAlarmInfo(a *compositeAlarmData) driver.CompositeAlarmInfo {
	return driver.CompositeAlarmInfo{
		Name:                               a.Name,
		ARN:                                a.ARN,
		AlarmRule:                          a.AlarmRule,
		AlarmDescription:                   a.AlarmDescription,
		State:                              a.State,
		StateReason:                        a.StateReason,
		StateReasonData:                    a.StateReasonData,
		StateUpdatedTimestamp:              a.StateUpdatedTimestamp,
		StateTransitionedTimestamp:         a.StateTransitionedTimestamp,
		AlarmConfigurationUpdatedTimestamp: a.ConfigUpdatedAt,
		ActionsEnabled:                     a.ActionsEnabled,
		AlarmActions:                       append([]string{}, a.AlarmActions...),
		OKActions:                          append([]string{}, a.OKActions...),
		InsufficientDataActions:            append([]string{}, a.InsufficientDataActions...),
		ActionsSuppressor:                  a.ActionsSuppressor,
		ActionsSuppressorWaitPeriod:        a.WaitPeriod,
		ActionsSuppressorExtensionPeriod:   a.ExtensionPeriod,
		ActionsSuppressedBy:                a.ActionsSuppressedBy,
		ActionsSuppressedReason:            a.ActionsSuppressedReason,
	}
}
