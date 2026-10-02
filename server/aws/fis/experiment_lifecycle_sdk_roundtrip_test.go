package fis_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	fisapi "github.com/aws/aws-sdk-go-v2/service/fis"
	fistypes "github.com/aws/aws-sdk-go-v2/service/fis/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2/config"
)

// TestSDKExperimentCompletes asserts an experiment reaches the completed state
// over the wire once its actions' durations elapse (the sample template's
// action runs PT2M), after which StopExperiment is a ConflictException.
func TestSDKExperimentCompletes(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	c := newClient(t, config.WithClock(fc))
	tpl := createTemplate(t, c)

	started, err := c.StartExperiment(ctx, &fisapi.StartExperimentInput{
		ClientToken: aws.String("tok-complete"), ExperimentTemplateId: tpl.Id,
	})
	if err != nil {
		t.Fatalf("StartExperiment: %v", err)
	}

	fc.Advance(2 * time.Minute)

	got, err := c.GetExperiment(ctx, &fisapi.GetExperimentInput{Id: started.Experiment.Id})
	if err != nil {
		t.Fatalf("GetExperiment: %v", err)
	}

	if got.Experiment.State.Status != fistypes.ExperimentStatusCompleted || got.Experiment.EndTime == nil {
		t.Fatalf("state = %+v endTime = %v, want completed with endTime", got.Experiment.State, got.Experiment.EndTime)
	}

	if st := got.Experiment.Actions["inject"].State.Status; st != fistypes.ExperimentActionStatusCompleted {
		t.Fatalf("action status = %q, want completed", st)
	}

	_, err = c.StopExperiment(ctx, &fisapi.StopExperimentInput{Id: started.Experiment.Id})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ConflictException" {
		t.Fatalf("StopExperiment on completed experiment err = %v, want ConflictException", err)
	}
}

// TestSDKStopExperimentWhileInitiating asserts that with AsyncSettle enabled
// StartExperiment reports initiating and StopExperiment is accepted in that
// state, cancelling the not-yet-started action.
func TestSDKStopExperimentWhileInitiating(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	c := newClient(t, config.WithClock(fc), config.WithAsyncSettle())
	tpl := createTemplate(t, c)

	started, err := c.StartExperiment(ctx, &fisapi.StartExperimentInput{
		ClientToken: aws.String("tok-init"), ExperimentTemplateId: tpl.Id,
	})
	if err != nil {
		t.Fatalf("StartExperiment: %v", err)
	}

	if st := started.Experiment.State.Status; st != fistypes.ExperimentStatusInitiating {
		t.Fatalf("start status = %q, want initiating", st)
	}

	stopped, err := c.StopExperiment(ctx, &fisapi.StopExperimentInput{Id: started.Experiment.Id})
	if err != nil {
		t.Fatalf("StopExperiment while initiating: %v", err)
	}

	if st := stopped.Experiment.State.Status; st != fistypes.ExperimentStatusStopped {
		t.Fatalf("stop status = %q, want stopped", st)
	}

	if st := stopped.Experiment.Actions["inject"].State.Status; st != fistypes.ExperimentActionStatusCancelled {
		t.Fatalf("action status = %q, want cancelled", st)
	}
}
