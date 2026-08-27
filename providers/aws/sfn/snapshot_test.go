package sfn_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// TestSnapshotRoundTripSFN proves a snapshot/restore round-trip preserves the
// Step Functions mock's state under the original identities: a state machine,
// an activity, and a completed execution (each promoted out of its mutex-guarded
// wrapper) survive restore into a fresh mock, and a re-snapshot is byte-identical.
func TestSnapshotRoundTripSFN(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	smARN, _, _, err := src.CreateStateMachine(ctx, driver.CreateStateMachineInput{
		Name:       "sm-1",
		Definition: `{"StartAt":"a","States":{"a":{"Type":"Pass","End":true}}}`,
		RoleArn:    "arn:aws:iam::000000000000:role/r",
		Type:       "STANDARD",
	})
	if err != nil {
		t.Fatalf("create state machine: %v", err)
	}

	actARN, _, err := src.CreateActivity(ctx, "act-1", nil)
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}

	exec, err := src.StartExecution(ctx, driver.StartExecutionInput{
		StateMachineArn: smARN, Name: "run-1", Input: `{"k":"v"}`,
	})
	if err != nil {
		t.Fatalf("start execution: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	sm, err := dst.DescribeStateMachine(ctx, smARN)
	if err != nil {
		t.Fatalf("describe restored state machine: %v", err)
	}

	if sm.Name != "sm-1" {
		t.Fatalf("restored state machine name = %q, want sm-1", sm.Name)
	}

	act, err := dst.DescribeActivity(ctx, actARN)
	if err != nil {
		t.Fatalf("describe restored activity: %v", err)
	}

	if act.Name != "act-1" {
		t.Fatalf("restored activity name = %q, want act-1", act.Name)
	}

	gotExec, err := dst.DescribeExecution(ctx, exec.ARN)
	if err != nil {
		t.Fatalf("describe restored execution: %v", err)
	}

	if gotExec.Name != "run-1" {
		t.Fatalf("restored execution name = %q, want run-1", gotExec.Name)
	}

	raw2, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-snapshot: %v", err)
	}

	if !bytes.Equal(raw, raw2) {
		t.Fatalf("re-snapshot not byte-identical to original")
	}
}
