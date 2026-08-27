package cloudwatch

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// compositeAlarmData is a stored composite alarm. Its AlarmRule is a boolean
// expression over other alarms' states. The rule is round-tripped faithfully so
// Terraform's aws_cloudwatch_composite_alarm never drifts; the state is not
// re-evaluated against child-alarm transitions (see PutCompositeAlarm).
type compositeAlarmData struct {
	Name                    string
	ARN                     string
	AlarmRule               string
	AlarmDescription        string
	State                   string
	StateReason             string
	StateUpdatedTimestamp   time.Time
	ActionsEnabled          bool
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	Tags                    map[string]string
}

// PutCompositeAlarm creates or updates a composite alarm. The AlarmRule and
// actions are stored verbatim. The alarm's StateValue is left at
// INSUFFICIENT_DATA on create: cloudemu does not run the boolean rule engine
// against child-alarm states (there is no trigger that re-evaluates a composite
// when a referenced alarm transitions), so rather than ship a half-working
// evaluator the definition is round-tripped faithfully. Updating an existing
// composite alarm preserves its current state and tags, matching PutMetricAlarm.
//
//nolint:gocritic // hugeParam: matches the optional-capability interface signature.
func (m *Mock) PutCompositeAlarm(_ context.Context, cfg driver.CompositeAlarmConfig) error {
	if cfg.Name == "" {
		return errors.Newf(errors.InvalidArgument, "alarm name is required")
	}

	if strings.TrimSpace(cfg.AlarmRule) == "" {
		return errors.Newf(errors.InvalidArgument, "alarm rule is required")
	}

	actionsEnabled := true
	if cfg.ActionsEnabled != nil {
		actionsEnabled = *cfg.ActionsEnabled
	}

	state := stateInsufficientData
	stateReason := "Unchecked: Initial alarm creation"
	stateUpdated := m.opts.Clock.Now()

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	if existing, ok := m.compositeAlarms.Get(cfg.Name); ok {
		state = existing.State
		stateReason = existing.StateReason
		stateUpdated = existing.StateUpdatedTimestamp
		tags = existing.Tags
	}

	m.compositeAlarms.Set(cfg.Name, &compositeAlarmData{
		Name:                    cfg.Name,
		ARN:                     idgen.AWSARN("cloudwatch", m.opts.Region, m.opts.AccountID, "alarm:"+cfg.Name),
		AlarmRule:               cfg.AlarmRule,
		AlarmDescription:        cfg.AlarmDescription,
		State:                   state,
		StateReason:             stateReason,
		StateUpdatedTimestamp:   stateUpdated,
		ActionsEnabled:          actionsEnabled,
		AlarmActions:            append([]string{}, cfg.AlarmActions...),
		OKActions:               append([]string{}, cfg.OKActions...),
		InsufficientDataActions: append([]string{}, cfg.InsufficientDataActions...),
		Tags:                    tags,
	})

	return nil
}

// DescribeCompositeAlarms returns composite alarms matching the given names, or
// all composite alarms when names is empty.
func (m *Mock) DescribeCompositeAlarms(_ context.Context, names []string) ([]driver.CompositeAlarmInfo, error) {
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

// DeleteCompositeAlarms deletes every named composite alarm that exists,
// silently skipping names that are absent. DeleteAlarms accepts both metric and
// composite alarm names in one call, so a name that is not a composite alarm is
// not an error here.
func (m *Mock) DeleteCompositeAlarms(_ context.Context, names []string) error {
	for _, name := range names {
		m.compositeAlarms.Delete(name)
	}

	return nil
}

func toCompositeAlarmInfo(a *compositeAlarmData) driver.CompositeAlarmInfo {
	return driver.CompositeAlarmInfo{
		Name:                    a.Name,
		ARN:                     a.ARN,
		AlarmRule:               a.AlarmRule,
		AlarmDescription:        a.AlarmDescription,
		State:                   a.State,
		StateReason:             a.StateReason,
		StateUpdatedTimestamp:   a.StateUpdatedTimestamp,
		ActionsEnabled:          a.ActionsEnabled,
		AlarmActions:            append([]string{}, a.AlarmActions...),
		OKActions:               append([]string{}, a.OKActions...),
		InsufficientDataActions: append([]string{}, a.InsufficientDataActions...),
	}
}
