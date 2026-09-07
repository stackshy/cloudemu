package eventbridgescheduler_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/eventbridgescheduler"
	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

func newMock() *eventbridgescheduler.Mock {
	return eventbridgescheduler.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func scheduleInput(name, group string) *driver.ScheduleInput {
	return &driver.ScheduleInput{
		Name:               name,
		GroupName:          group,
		ScheduleExpression: "rate(1 hour)",
		FlexibleTimeWindow: json.RawMessage(`{"Mode":"OFF"}`),
		Target: json.RawMessage(
			`{"Arn":"arn:aws:sqs:us-east-1:123456789012:q","RoleArn":"arn:aws:iam::123456789012:role/r"}`),
	}
}

func TestCreateScheduleComputedFields(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	out, err := m.CreateSchedule(ctx, scheduleInput("s1", ""))
	requireNoError(t, err)

	wantArn := "arn:aws:scheduler:us-east-1:" + config.NewOptions().AccountID + ":schedule/default/s1"
	if out.Arn != wantArn {
		t.Fatalf("arn = %q, want %q", out.Arn, wantArn)
	}

	if out.GroupName != "default" {
		t.Fatalf("groupName = %q, want default", out.GroupName)
	}

	if out.State != driver.ScheduleEnabled {
		t.Fatalf("state = %q, want ENABLED", out.State)
	}

	if out.CreationDate.IsZero() || !out.CreationDate.Equal(out.LastModificationDate) {
		t.Fatalf("creation/modification timestamps not set consistently at create")
	}
}

func TestCreateScheduleStableAcrossReads(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateSchedule(ctx, scheduleInput("s1", ""))
	requireNoError(t, err)

	got, err := m.GetSchedule(ctx, "", "s1")
	requireNoError(t, err)

	if got.Arn != created.Arn || !got.CreationDate.Equal(created.CreationDate) {
		t.Fatalf("computed fields drifted between create and get")
	}

	if string(got.FlexibleTimeWindow) != `{"Mode":"OFF"}` {
		t.Fatalf("flexible time window not round-tripped verbatim: %s", got.FlexibleTimeWindow)
	}

	if !strings.Contains(string(got.Target), `"RoleArn"`) {
		t.Fatalf("target not round-tripped verbatim: %s", got.Target)
	}
}

func TestCreateScheduleDuplicateConflicts(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateSchedule(ctx, scheduleInput("s1", ""))
	requireNoError(t, err)

	_, err = m.CreateSchedule(ctx, scheduleInput("s1", ""))
	assertException(t, err, driver.ExConflict)
}

func TestCreateScheduleMissingGroup(t *testing.T) {
	m := newMock()

	_, err := m.CreateSchedule(context.Background(), scheduleInput("s1", "nope"))
	assertException(t, err, driver.ExResourceNotFound)
}

func TestCreateScheduleValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	in := scheduleInput("s1", "")
	in.ScheduleExpression = ""

	_, err := m.CreateSchedule(ctx, in)
	assertException(t, err, driver.ExValidation)
}

func TestUpdateScheduleReplacesAndBumpsModified(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateSchedule(ctx, scheduleInput("s1", ""))
	requireNoError(t, err)

	upd := scheduleInput("s1", "")
	upd.Description = "changed"
	upd.State = driver.ScheduleDisabled

	got, err := m.UpdateSchedule(ctx, upd)
	requireNoError(t, err)

	if got.Description != "changed" || got.State != driver.ScheduleDisabled {
		t.Fatalf("update did not replace fields: %+v", got)
	}

	if got.Arn != created.Arn || !got.CreationDate.Equal(created.CreationDate) {
		t.Fatalf("update drifted arn/creationDate")
	}
}

func TestUpdateScheduleNotFound(t *testing.T) {
	m := newMock()

	_, err := m.UpdateSchedule(context.Background(), scheduleInput("ghost", ""))
	assertException(t, err, driver.ExResourceNotFound)
}

func TestDeleteSchedule(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateSchedule(ctx, scheduleInput("s1", ""))
	requireNoError(t, err)

	requireNoError(t, m.DeleteSchedule(ctx, "", "s1"))

	_, err = m.GetSchedule(ctx, "", "s1")
	assertException(t, err, driver.ExResourceNotFound)

	assertException(t, m.DeleteSchedule(ctx, "", "s1"), driver.ExResourceNotFound)
}

func TestListSchedulesFilters(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateScheduleGroup(ctx, "g1", nil)
	requireNoError(t, err)

	_, err = m.CreateSchedule(ctx, scheduleInput("alpha", ""))
	requireNoError(t, err)
	_, err = m.CreateSchedule(ctx, scheduleInput("beta", "g1"))
	requireNoError(t, err)

	all, _, err := m.ListSchedules(ctx, driver.ScheduleFilter{})
	requireNoError(t, err)

	if len(all) != 2 {
		t.Fatalf("list all = %d, want 2", len(all))
	}

	byGroup, _, err := m.ListSchedules(ctx, driver.ScheduleFilter{GroupName: "g1"})
	requireNoError(t, err)

	if len(byGroup) != 1 || byGroup[0].Name != "beta" {
		t.Fatalf("group filter = %+v, want only beta", byGroup)
	}

	byPrefix, _, err := m.ListSchedules(ctx, driver.ScheduleFilter{NamePrefix: "alp"})
	requireNoError(t, err)

	if len(byPrefix) != 1 || byPrefix[0].Name != "alpha" {
		t.Fatalf("prefix filter = %+v, want only alpha", byPrefix)
	}
}

func assertException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if err == nil || !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError %s", err, want)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}
}
