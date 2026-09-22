package eventbridgescheduler_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

func TestCreateScheduleClientTokenReplaysOriginal(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := scheduleInput("s1", "")
	in.ClientToken = "tok-1"

	first, err := m.CreateSchedule(ctx, in)
	requireNoError(t, err)

	// A retried create resends the same name: it must return the original
	// schedule, not a false ConflictException.
	retry, err := m.CreateSchedule(ctx, in)
	requireNoError(t, err)

	if retry.Arn != first.Arn || !retry.CreationDate.Equal(first.CreationDate) {
		t.Fatalf("same-token retry = %s @%v, want original %s @%v", retry.Arn, retry.CreationDate, first.Arn, first.CreationDate)
	}

	// Same name, different token: a genuine duplicate that must conflict.
	in.ClientToken = "tok-2"
	if _, err = m.CreateSchedule(ctx, in); err == nil {
		t.Fatal("same name with a different token must conflict")
	}

	// Same token reused on a different schedule must not replay s1.
	in2 := scheduleInput("s2", "")
	in2.ClientToken = "tok-1"

	other, err := m.CreateSchedule(ctx, in2)
	requireNoError(t, err)

	if other.Arn == first.Arn {
		t.Fatalf("token reused on a different schedule replayed %s", first.Arn)
	}

	list, _, err := m.ListSchedules(ctx, driver.ScheduleFilter{})
	requireNoError(t, err)

	if len(list) != 2 {
		t.Fatalf("schedule count = %d, want 2", len(list))
	}
}

func TestCreateScheduleClientTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := scheduleInput("s1", "")
	in.ClientToken = "tok-1"

	_, err := m.CreateSchedule(ctx, in)
	requireNoError(t, err)
	requireNoError(t, m.DeleteSchedule(ctx, "", "s1"))

	if _, err = m.CreateSchedule(ctx, in); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}

	if _, err = m.GetSchedule(ctx, "", "s1"); err != nil {
		t.Fatalf("schedule should exist after recreate: %v", err)
	}
}

func TestCreateScheduleClientTokenAfterUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := scheduleInput("s1", "")
	in.ClientToken = "g1"

	_, err := m.CreateSchedule(ctx, in)
	requireNoError(t, err)

	upd := scheduleInput("s1", "")
	upd.ScheduleExpression = "rate(5 minutes)"
	_, err = m.UpdateSchedule(ctx, upd)
	requireNoError(t, err)

	retry, err := m.CreateSchedule(ctx, in)
	requireNoError(t, err)

	if retry.ScheduleExpression != "rate(5 minutes)" {
		t.Fatalf("replay expression = %q, want the live %q", retry.ScheduleExpression, "rate(5 minutes)")
	}
}

func TestCreateScheduleClientTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	const n = 20

	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			in := scheduleInput("s1", "")
			in.ClientToken = "burst"
			_, errs[i] = m.CreateSchedule(ctx, in)
		}()
	}

	close(start)
	wg.Wait()

	// All callers share one token, so each is a retry of the same create and
	// none may see a ConflictException raised by its sibling.
	for i := range n {
		requireNoError(t, errs[i])
	}

	list, _, err := m.ListSchedules(ctx, driver.ScheduleFilter{})
	requireNoError(t, err)

	if len(list) != 1 {
		t.Fatalf("schedule count = %d, want 1", len(list))
	}
}
