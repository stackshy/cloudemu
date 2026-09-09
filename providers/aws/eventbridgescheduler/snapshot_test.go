package eventbridgescheduler_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/eventbridgescheduler"
)

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	g, err := src.CreateScheduleGroup(ctx, "g1", map[string]string{"a": "1"})
	requireNoError(t, err)

	sched, err := src.CreateSchedule(ctx, scheduleInput("s1", "g1"))
	requireNoError(t, err)

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := eventbridgescheduler.New(config.NewOptions())
	requireNoError(t, dst.Restore(ctx, data))

	gotSched, err := dst.GetSchedule(ctx, "g1", "s1")
	requireNoError(t, err)

	if gotSched.Arn != sched.Arn || string(gotSched.Target) != string(sched.Target) {
		t.Fatalf("restored schedule does not match original identity")
	}

	gotGroup, err := dst.GetScheduleGroup(ctx, "g1")
	requireNoError(t, err)

	if gotGroup.Arn != g.Arn || gotGroup.Tags["a"] != "1" {
		t.Fatalf("restored group does not match original identity")
	}
}

func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	data, err := newMock().Snapshot(ctx, false)
	requireNoError(t, err)

	if err := eventbridgescheduler.New(config.NewOptions()).Restore(ctx, data); err != nil {
		t.Fatalf("restore empty snapshot: %v", err)
	}
}
