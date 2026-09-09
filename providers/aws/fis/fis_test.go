package fis_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/fis"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

func newMock() *fis.Mock {
	return fis.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func sampleCreateInput() *driver.CreateExperimentTemplateInput {
	return &driver.CreateExperimentTemplateInput{
		ClientToken: "tok-1",
		Description: "network latency test",
		RoleArn:     "arn:aws:iam::123456789012:role/fis-role",
		Actions: map[string]driver.Action{
			"inject": {
				ActionID:   "aws:ec2:stop-instances",
				Parameters: map[string]string{"startInstancesAfterDuration": "PT2M"},
				Targets:    map[string]string{"Instances": "targetInstances"},
			},
		},
		Targets: map[string]driver.Target{
			"targetInstances": {
				ResourceType:  "aws:ec2:instance",
				SelectionMode: "COUNT(1)",
				ResourceTags:  map[string]string{"env": "test"},
			},
		},
		StopConditions: []driver.StopCondition{{Source: "none"}},
	}
}

func mustTemplate(t *testing.T, m *fis.Mock) *driver.ExperimentTemplate {
	t.Helper()

	tpl, err := m.CreateExperimentTemplate(context.Background(), sampleCreateInput())
	requireNoError(t, err)

	return tpl
}

func TestCreateTemplateComputedFields(t *testing.T) {
	m := newMock()
	tpl := mustTemplate(t, m)

	if !strings.HasPrefix(tpl.ID, "EXT") {
		t.Fatalf("id = %q, want EXT prefix", tpl.ID)
	}

	if !strings.HasSuffix(tpl.Arn, ":experiment-template/"+tpl.ID) {
		t.Fatalf("arn = %q, want experiment-template/%s suffix", tpl.Arn, tpl.ID)
	}

	if tpl.CreationTime.IsZero() || tpl.LastUpdateTime.IsZero() {
		t.Fatal("creationTime/lastUpdateTime is zero")
	}

	if tpl.ExperimentOptions.AccountTargeting != "single-account" ||
		tpl.ExperimentOptions.EmptyTargetResolutionMode != "fail" {
		t.Fatalf("experiment options defaults not applied: %+v", tpl.ExperimentOptions)
	}
}

func TestCreateTemplateValidation(t *testing.T) {
	m := newMock()

	cases := map[string]func(*driver.CreateExperimentTemplateInput){
		"no description":    func(in *driver.CreateExperimentTemplateInput) { in.Description = "" },
		"no role":           func(in *driver.CreateExperimentTemplateInput) { in.RoleArn = "" },
		"no stop condition": func(in *driver.CreateExperimentTemplateInput) { in.StopConditions = nil },
		"no action":         func(in *driver.CreateExperimentTemplateInput) { in.Actions = nil },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := sampleCreateInput()
			mutate(in)

			if _, err := m.CreateExperimentTemplate(context.Background(), in); !cerrors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestGetTemplateStableAcrossReads(t *testing.T) {
	m := newMock()
	tpl := mustTemplate(t, m)

	got1, err := m.GetExperimentTemplate(context.Background(), tpl.ID)
	requireNoError(t, err)

	got2, err := m.GetExperimentTemplate(context.Background(), tpl.ID)
	requireNoError(t, err)

	if got1.Arn != got2.Arn || !got1.CreationTime.Equal(got2.CreationTime) ||
		!got1.LastUpdateTime.Equal(got2.LastUpdateTime) {
		t.Fatal("computed fields drifted across reads")
	}

	// The action and target blocks round-trip verbatim.
	if got1.Actions["inject"].ActionID != "aws:ec2:stop-instances" {
		t.Fatalf("action id not preserved: %+v", got1.Actions)
	}

	if got1.Targets["targetInstances"].SelectionMode != "COUNT(1)" {
		t.Fatalf("target block not preserved: %+v", got1.Targets)
	}
}

func TestGetTemplateNotFound(t *testing.T) {
	m := newMock()

	if _, err := m.GetExperimentTemplate(context.Background(), "EXTmissing"); !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestUpdateTemplateReplacesFieldsAndAdvancesTime(t *testing.T) {
	m := newMock()
	tpl := mustTemplate(t, m)

	newDesc := "updated description"
	updated, err := m.UpdateExperimentTemplate(context.Background(), &driver.UpdateExperimentTemplateInput{
		ID:          tpl.ID,
		Description: &newDesc,
	})
	requireNoError(t, err)

	if updated.Description != newDesc {
		t.Fatalf("description = %q, want %q", updated.Description, newDesc)
	}

	if updated.ID != tpl.ID || updated.Arn != tpl.Arn || !updated.CreationTime.Equal(tpl.CreationTime) {
		t.Fatal("identity/creationTime changed on update")
	}

	// Absent fields are left unchanged.
	if updated.RoleArn != tpl.RoleArn || len(updated.Actions) != 1 {
		t.Fatalf("unchanged fields lost: %+v", updated)
	}
}

func TestDeleteTemplate(t *testing.T) {
	m := newMock()
	tpl := mustTemplate(t, m)

	_, err := m.DeleteExperimentTemplate(context.Background(), tpl.ID)
	requireNoError(t, err)

	if _, err := m.GetExperimentTemplate(context.Background(), tpl.ID); !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound after delete", err)
	}
}

func TestListTemplates(t *testing.T) {
	m := newMock()
	mustTemplate(t, m)
	mustTemplate(t, m)

	templates, next, err := m.ListExperimentTemplates(context.Background(), driver.Page{})
	requireNoError(t, err)

	if len(templates) != 2 || next != "" {
		t.Fatalf("got %d templates next=%q, want 2 and empty", len(templates), next)
	}
}

func TestExperimentLifecycle(t *testing.T) {
	m := newMock()
	tpl := mustTemplate(t, m)
	ctx := context.Background()

	exp, err := m.StartExperiment(ctx, &driver.StartExperimentInput{
		ClientToken:          "tok-2",
		ExperimentTemplateID: tpl.ID,
	})
	requireNoError(t, err)

	if !strings.HasPrefix(exp.ID, "EXP") {
		t.Fatalf("experiment id = %q, want EXP prefix", exp.ID)
	}

	if exp.State.Status != "running" {
		t.Fatalf("state = %q, want running", exp.State.Status)
	}

	if exp.ExperimentTemplateID != tpl.ID || exp.RoleArn != tpl.RoleArn {
		t.Fatal("experiment did not inherit template identity")
	}

	if exp.ExperimentOptions.ActionsMode != "run-all" {
		t.Fatalf("actionsMode = %q, want run-all", exp.ExperimentOptions.ActionsMode)
	}

	// GetExperiment reports the same running state.
	got, err := m.GetExperiment(ctx, exp.ID)
	requireNoError(t, err)

	if got.State.Status != "running" || got.StartTime.IsZero() {
		t.Fatalf("get returned %+v", got.State)
	}

	// StopExperiment moves it to the stopped terminal state.
	stopped, err := m.StopExperiment(ctx, exp.ID)
	requireNoError(t, err)

	if stopped.State.Status != "stopped" || stopped.EndTime.IsZero() {
		t.Fatalf("stopped state = %+v endTime=%v", stopped.State, stopped.EndTime)
	}

	if stopped.Actions["inject"].State.Status != "stopped" {
		t.Fatalf("action state not stopped: %+v", stopped.Actions["inject"])
	}

	// Stopping an already-terminal experiment is a conflict.
	if _, err := m.StopExperiment(ctx, exp.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("second stop err = %v, want FailedPrecondition", err)
	}
}

func TestStartExperimentUnknownTemplate(t *testing.T) {
	m := newMock()

	_, err := m.StartExperiment(context.Background(), &driver.StartExperimentInput{ExperimentTemplateID: "EXTmissing"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestTagging(t *testing.T) {
	m := newMock()
	tpl := mustTemplate(t, m)
	ctx := context.Background()

	requireNoError(t, m.TagResource(ctx, tpl.Arn, map[string]string{"team": "chaos", "env": "dev"}))

	tags, err := m.ListTagsForResource(ctx, tpl.Arn)
	requireNoError(t, err)

	if tags["team"] != "chaos" || tags["env"] != "dev" {
		t.Fatalf("tags = %+v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, tpl.Arn, []string{"env"}))

	tags, err = m.ListTagsForResource(ctx, tpl.Arn)
	requireNoError(t, err)

	if _, ok := tags["env"]; ok || tags["team"] != "chaos" {
		t.Fatalf("after untag tags = %+v", tags)
	}
}

func TestTagUnknownResource(t *testing.T) {
	m := newMock()

	err := m.TagResource(context.Background(),
		"arn:aws:fis:us-east-1:123456789012:experiment-template/EXTmissing", map[string]string{"a": "b"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}
