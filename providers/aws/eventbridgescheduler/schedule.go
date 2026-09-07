package eventbridgescheduler

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// CreateSchedule creates a schedule inside its (defaulted) group with a stable
// arn and creation timestamp. The group must exist; the target,
// flexible-time-window and start/end dates are carried verbatim.
func (m *Mock) CreateSchedule(_ context.Context, in *driver.ScheduleInput) (*driver.Schedule, error) {
	if err := validateScheduleInput(in); err != nil {
		return nil, err
	}

	group := resolveGroup(in.GroupName)
	if !m.groupExists(group) {
		return nil, notFound("schedule group %q does not exist", group)
	}

	key := scheduleKey(group, in.Name)
	if m.schedules.Has(key) {
		return nil, conflict("schedule %q already exists in group %q", in.Name, group)
	}

	now := m.now()
	sched := m.buildSchedule(in, group, now, now)
	m.schedules.Set(key, sched)

	out := copySchedule(&sched)

	return &out, nil
}

// GetSchedule returns a copy of the schedule. The stored computed fields are
// returned unchanged so repeated reads never drift.
func (m *Mock) GetSchedule(_ context.Context, group, name string) (*driver.Schedule, error) {
	group = resolveGroup(group)

	s, ok := m.schedules.Get(scheduleKey(group, name))
	if !ok {
		return nil, notFound("schedule %q does not exist in group %q", name, group)
	}

	out := copySchedule(&s)

	return &out, nil
}

// UpdateSchedule replaces the schedule with the supplied definition (PUT
// semantics). The arn and creation timestamp are preserved; the last
// modification timestamp is bumped.
func (m *Mock) UpdateSchedule(_ context.Context, in *driver.ScheduleInput) (*driver.Schedule, error) {
	if err := validateScheduleInput(in); err != nil {
		return nil, err
	}

	group := resolveGroup(in.GroupName)
	key := scheduleKey(group, in.Name)

	existing, ok := m.schedules.Get(key)
	if !ok {
		return nil, notFound("schedule %q does not exist in group %q", in.Name, group)
	}

	sched := m.buildSchedule(in, group, existing.CreationDate, m.now())
	m.schedules.Set(key, sched)

	out := copySchedule(&sched)

	return &out, nil
}

// DeleteSchedule removes a schedule. A subsequent GetSchedule yields
// ResourceNotFoundException so an IaC delete waiter completes.
func (m *Mock) DeleteSchedule(_ context.Context, group, name string) error {
	group = resolveGroup(group)

	if !m.schedules.Delete(scheduleKey(group, name)) {
		return notFound("schedule %q does not exist in group %q", name, group)
	}

	return nil
}

// ListSchedules returns a deterministic page of schedule summaries ordered by
// key, filtered by group, name prefix and state.
func (m *Mock) ListSchedules(_ context.Context, f driver.ScheduleFilter) (schedules []driver.Schedule, nextToken string, err error) {
	stored := m.schedules.SortedValues()

	var matched []driver.Schedule

	for i := range stored {
		s := &stored[i]

		if f.GroupName != "" && s.GroupName != f.GroupName {
			continue
		}

		if !hasPrefix(s.Name, f.NamePrefix) {
			continue
		}

		if f.State != "" && s.State != f.State {
			continue
		}

		matched = append(matched, *s)
	}

	start, end, next := paginate(len(matched), f.Page)

	out := make([]driver.Schedule, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copySchedule(&matched[i]))
	}

	return out, next, nil
}

// buildSchedule assembles a stored schedule from an input and the create/modify
// timestamps, minting the stable arn and defaulting the state to ENABLED.
func (m *Mock) buildSchedule(in *driver.ScheduleInput, group string, created, modified time.Time) driver.Schedule {
	state := in.State
	if state == "" {
		state = driver.ScheduleEnabled
	}

	return driver.Schedule{
		Name:                       in.Name,
		GroupName:                  group,
		Arn:                        m.scheduleARN(group, in.Name),
		ActionAfterCompletion:      in.ActionAfterCompletion,
		Description:                in.Description,
		ScheduleExpression:         in.ScheduleExpression,
		ScheduleExpressionTimezone: in.ScheduleExpressionTimezone,
		State:                      state,
		KmsKeyArn:                  in.KmsKeyArn,
		StartDate:                  copyRaw(in.StartDate),
		EndDate:                    copyRaw(in.EndDate),
		FlexibleTimeWindow:         copyRaw(in.FlexibleTimeWindow),
		Target:                     copyRaw(in.Target),
		CreationDate:               created,
		LastModificationDate:       modified,
	}
}

// validateScheduleInput enforces the required fields shared by create and
// update.
func validateScheduleInput(in *driver.ScheduleInput) error {
	if in.Name == "" {
		return validation("schedule name is required")
	}

	if in.ScheduleExpression == "" {
		return validation("ScheduleExpression is required")
	}

	if len(in.FlexibleTimeWindow) == 0 {
		return validation("FlexibleTimeWindow is required")
	}

	if len(in.Target) == 0 {
		return validation("Target is required")
	}

	return nil
}
