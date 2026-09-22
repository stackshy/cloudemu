package fis_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/fis"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

var lifecycleStart = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed test epoch

// chainedTemplate creates a template whose "inject" action (PT2M) starts after
// "wait" (PT1M), so the experiment runs for three minutes.
func chainedTemplate(t *testing.T, m *fis.Mock) *driver.ExperimentTemplate {
	t.Helper()

	in := sampleCreateInput()
	in.Actions = map[string]driver.Action{
		"wait":   {ActionID: "aws:fis:wait", Parameters: map[string]string{"duration": "PT1M"}},
		"inject": {ActionID: "aws:fis:wait", Parameters: map[string]string{"duration": "PT2M"}, StartAfter: []string{"wait"}},
	}

	tpl, err := m.CreateExperimentTemplate(context.Background(), in)
	requireNoError(t, err)

	return tpl
}

func requireStatus(t *testing.T, e *driver.Experiment, want string, actions map[string]string) {
	t.Helper()

	if e.State.Status != want {
		t.Fatalf("experiment status = %q, want %q", e.State.Status, want)
	}

	for name, st := range actions {
		if got := e.Actions[name].State.Status; got != st {
			t.Fatalf("action %s status = %q, want %q", name, got, st)
		}
	}
}

// TestExperimentRunsToCompletion covers the running -> completed transition
// derived from the actions' durations and startAfter ordering: completed is
// reachable, and a completed experiment can no longer be stopped.
func TestExperimentRunsToCompletion(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(lifecycleStart)
	m := fis.New(config.NewOptions(config.WithClock(fc)))
	tpl := chainedTemplate(t, m)

	exp, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID})
	requireNoError(t, err)
	requireStatus(t, exp, "running", map[string]string{"wait": "running", "inject": "pending"})

	fc.Advance(90 * time.Second)

	got, err := m.GetExperiment(ctx, exp.ID)
	requireNoError(t, err)
	requireStatus(t, got, "running", map[string]string{"wait": "completed", "inject": "running"})

	fc.Advance(90 * time.Second)

	got, err = m.GetExperiment(ctx, exp.ID)
	requireNoError(t, err)
	requireStatus(t, got, "completed", map[string]string{"wait": "completed", "inject": "completed"})

	if want := lifecycleStart.Add(3 * time.Minute); !got.EndTime.Equal(want) {
		t.Fatalf("endTime = %v, want %v", got.EndTime, want)
	}

	list, _, err := m.ListExperiments(ctx, driver.Page{})
	requireNoError(t, err)

	if len(list) != 1 || list[0].State.Status != "completed" {
		t.Fatalf("ListExperiments = %+v, want one completed experiment", list)
	}

	if _, err = m.StopExperiment(ctx, exp.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("StopExperiment on completed experiment: err = %v, want conflict", err)
	}
}

// TestStopExperimentRightAfterStart asserts a stop issued immediately after
// start stops running actions, cancels actions that had not started, and is
// persisted (the experiment never later reports completed).
func TestStopExperimentRightAfterStart(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(lifecycleStart)
	m := fis.New(config.NewOptions(config.WithClock(fc)))
	tpl := chainedTemplate(t, m)

	exp, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID})
	requireNoError(t, err)

	stopped, err := m.StopExperiment(ctx, exp.ID)
	requireNoError(t, err)
	requireStatus(t, stopped, "stopped", map[string]string{"wait": "stopped", "inject": "cancelled"})

	fc.Advance(time.Hour)

	got, err := m.GetExperiment(ctx, exp.ID)
	requireNoError(t, err)
	requireStatus(t, got, "stopped", map[string]string{"wait": "stopped", "inject": "cancelled"})
}

// TestInstantExperimentStillStoppable asserts an experiment whose actions have
// no duration still runs for a minimum window (so start-then-stop works) and
// then completes on its own.
func TestInstantExperimentStillStoppable(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(lifecycleStart)
	m := fis.New(config.NewOptions(config.WithClock(fc)))

	in := sampleCreateInput()
	in.Actions = map[string]driver.Action{"reboot": {ActionID: "aws:ec2:reboot-instances"}}
	tpl, err := m.CreateExperimentTemplate(ctx, in)
	requireNoError(t, err)

	first, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID})
	requireNoError(t, err)
	requireStatus(t, first, "running", map[string]string{"reboot": "completed"})

	_, err = m.StopExperiment(ctx, first.ID)
	requireNoError(t, err)

	second, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID})
	requireNoError(t, err)

	fc.Advance(time.Minute)

	got, err := m.GetExperiment(ctx, second.ID)
	requireNoError(t, err)
	requireStatus(t, got, "completed", map[string]string{"reboot": "completed"})
}

// TestStopExperimentWhileInitiating asserts that with AsyncSettle enabled an
// experiment first reports initiating (actions pending), that StopExperiment
// is accepted in that state and cancels every action, and that an unstopped
// experiment moves on to running once the settle window elapses.
func TestStopExperimentWhileInitiating(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(lifecycleStart)
	m := fis.New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	tpl := chainedTemplate(t, m)

	exp, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID})
	requireNoError(t, err)
	requireStatus(t, exp, "initiating", map[string]string{"wait": "pending", "inject": "pending"})

	stopped, err := m.StopExperiment(ctx, exp.ID)
	requireNoError(t, err)
	requireStatus(t, stopped, "stopped", map[string]string{"wait": "cancelled", "inject": "cancelled"})

	other, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID})
	requireNoError(t, err)

	fc.Advance(2 * time.Second)

	got, err := m.GetExperiment(ctx, other.ID)
	requireNoError(t, err)
	requireStatus(t, got, "running", map[string]string{"wait": "running", "inject": "pending"})
}

// TestSkipAllExperimentSkipsActions asserts actionsMode skip-all reports every
// action skipped and the experiment completes after the minimum run window.
func TestSkipAllExperimentSkipsActions(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(lifecycleStart)
	m := fis.New(config.NewOptions(config.WithClock(fc)))
	tpl := chainedTemplate(t, m)

	exp, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID, ActionsMode: "skip-all"})
	requireNoError(t, err)
	requireStatus(t, exp, "running", map[string]string{"wait": "skipped", "inject": "skipped"})

	fc.Advance(time.Minute)

	got, err := m.GetExperiment(ctx, exp.ID)
	requireNoError(t, err)
	requireStatus(t, got, "completed", map[string]string{"wait": "skipped", "inject": "skipped"})
}
