package cloudwatch

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// initialStateReason is the reason AWS gives a newly created alarm.
const initialStateReason = "Unchecked: Initial alarm creation"

// CreateAlarm creates or updates an alarm. A new alarm starts in
// INSUFFICIENT_DATA and is evaluated at once. Both publish a configuration
// change event. An update keeps the current
// state and tags and overwrites the rest of the configuration. Its state is
// left unchanged, as PutMetricAlarm documents, and the next due evaluation
// uses the new configuration.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateAlarm(ctx context.Context, cfg driver.AlarmConfig) error {
	if cfg.Name == "" {
		return errors.Newf(errors.InvalidArgument, "alarm name is required")
	}

	now := m.opts.Clock.Now()
	alarm := m.newAlarmData(&cfg, now)

	m.alarmMu.Lock()

	existing, update := m.alarms.Get(cfg.Name)
	if update {
		alarm.State = existing.State
		alarm.StateReason = existing.StateReason
		alarm.StateReasonData = existing.StateReasonData
		alarm.StateUpdatedTimestamp = existing.StateUpdatedTimestamp
		alarm.StateTransitionedTimestamp = existing.StateTransitionedTimestamp
		alarm.LastEvaluatedAt = existing.LastEvaluatedAt
		alarm.Tags = existing.Tags
	}

	m.alarms.Set(cfg.Name, alarm)

	var (
		notice *alarmNotice
		config *alarmStateEvent
	)

	if update {
		config = configEventLocked(operationUpdate, alarm, existing)
	} else {
		config = configEventLocked(operationCreate, alarm, nil)
		notice = m.evaluateLocked(alarm, now)
	}

	m.alarmMu.Unlock()

	m.emitEvent(ctx, config)
	m.publish(ctx, notice)

	return nil
}

// newAlarmData builds a fresh alarm record from a PutMetricAlarm config.
func (m *Mock) newAlarmData(cfg *driver.AlarmConfig, now time.Time) *alarmData {
	actionsEnabled := true
	if cfg.ActionsEnabled != nil {
		actionsEnabled = *cfg.ActionsEnabled
	}

	return &alarmData{
		Name:                       cfg.Name,
		Namespace:                  cfg.Namespace,
		MetricName:                 cfg.MetricName,
		Dimensions:                 copyMap(cfg.Dimensions),
		ComparisonOperator:         cfg.ComparisonOperator,
		Threshold:                  cfg.Threshold,
		Period:                     cfg.Period,
		EvaluationPeriods:          cfg.EvaluationPeriods,
		DatapointsToAlarm:          cfg.DatapointsToAlarm,
		Stat:                       cfg.Stat,
		ExtendedStatistic:          cfg.ExtendedStatistic,
		Unit:                       cfg.Unit,
		TreatMissingData:           cfg.TreatMissingData,
		State:                      stateInsufficientData,
		StateReason:                initialStateReason,
		StateUpdatedTimestamp:      now,
		StateTransitionedTimestamp: now,
		AlarmActions:               append([]string{}, cfg.AlarmActions...),
		OKActions:                  append([]string{}, cfg.OKActions...),
		InsufficientDataActions:    append([]string{}, cfg.InsufficientDataActions...),
		AlarmDescription:           cfg.AlarmDescription,
		ActionsEnabled:             actionsEnabled,
		AlarmArn:                   idgen.AWSARN("cloudwatch", m.opts.Region, m.opts.AccountID, "alarm:"+cfg.Name),
		Tags:                       copyMap(cfg.Tags),
		MetricQueryID:              idgen.UUID(),
		ConfigUpdatedAt:            now,
	}
}

// copyMap returns a non-nil copy of a string map.
func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// DeleteAlarm deletes the alarm with the given name and publishes a
// configuration change event.
func (m *Mock) DeleteAlarm(ctx context.Context, name string) error {
	m.alarmMu.Lock()

	a, ok := m.alarms.Get(name)
	if !ok {
		m.alarmMu.Unlock()

		return errors.Newf(errors.NotFound, "alarm %q not found", name)
	}

	config := configEventLocked(operationDelete, a, nil)

	m.alarms.Delete(name)
	m.alarmMu.Unlock()

	m.emitEvent(ctx, config)

	return nil
}

// DescribeAlarms returns alarms matching the given names, or all alarms if
// names is empty. Alarms that are due are evaluated first.
func (m *Mock) DescribeAlarms(ctx context.Context, names []string) ([]driver.AlarmInfo, error) {
	m.evaluateDue(ctx, m.opts.Clock.Now())

	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	if len(names) == 0 {
		all := m.alarms.All()
		result := make([]driver.AlarmInfo, 0, len(all))

		for _, a := range all {
			result = append(result, toAlarmInfo(a))
		}

		return result, nil
	}

	result := make([]driver.AlarmInfo, 0, len(names))

	for _, name := range names {
		a, ok := m.alarms.Get(name)
		if !ok {
			continue
		}

		result = append(result, toAlarmInfo(a))
	}

	return result, nil
}

// SetAlarmState manually sets the state of an alarm. Like a metric-driven
// transition, a state change records a history entry and invokes the actions
// configured for the new state, so the documented "force ALARM to test
// wiring" workflow delivers its notifications.
func (m *Mock) SetAlarmState(ctx context.Context, name, state, reason string) error {
	return m.SetAlarmStateWithData(ctx, name, state, reason, "")
}

// SetAlarmStateWithData is SetAlarmState with the optional StateReasonData
// JSON. An empty reasonData clears any data from an earlier transition.
//
// The forced state is temporary. AWS says metric alarms "return to their
// actual state quickly". Here the state holds for one evaluation interval and
// the next due evaluation puts the real state back.
func (m *Mock) SetAlarmStateWithData(ctx context.Context, name, state, reason, reasonData string) error {
	if !alarmeval.ValidState(state) {
		return errors.Newf(errors.InvalidArgument, "invalid alarm state %q: must be OK, ALARM or INSUFFICIENT_DATA", state)
	}

	now := m.opts.Clock.Now()

	m.alarmMu.Lock()

	a, ok := m.alarms.Get(name)
	if !ok {
		m.alarmMu.Unlock()

		return errors.Newf(errors.NotFound, "alarm %q not found", name)
	}

	a.LastEvaluatedAt = now
	notice := m.transitionLocked(a, state, reason, reasonData, now)
	// A call that keeps the state still replaces the reason.
	a.StateReason = reason
	a.StateReasonData = reasonData
	m.alarmMu.Unlock()

	m.publish(ctx, notice)

	return nil
}
