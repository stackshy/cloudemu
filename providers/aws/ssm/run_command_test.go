package ssm_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// fakeFleet answers DescribeInstances from a fixed instance list.
type fakeFleet []computedriver.Instance

func (f fakeFleet) DescribeInstances(_ context.Context, ids []string, filters []computedriver.DescribeFilter,
	_ ...computedriver.DescribeInstancesOptions,
) ([]computedriver.Instance, error) {
	var out []computedriver.Instance

	for _, inst := range f {
		if len(ids) > 0 && !slices.Contains(ids, inst.ID) {
			continue
		}

		if len(filters) > 0 && !slices.Contains(filters[0].Values, inst.Tags["Role"]) {
			continue
		}

		out = append(out, inst)
	}

	return out, nil
}

const (
	webA = "i-0aaaaaaaaaaaaaaaa"
	webB = "i-0bbbbbbbbbbbbbbbb"
	webC = "i-0cccccccccccccccc"
)

func newSettledMock(t *testing.T) (*ssm.Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := ssm.New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	m.SetInstanceResolver(fakeFleet{
		{ID: webA, State: "running", Tags: map[string]string{"Role": "web"}},
		{ID: webB, State: "running", Tags: map[string]string{"Role": "web"}},
		{ID: webC, State: "stopped", Tags: map[string]string{"Role": "web"}},
	})

	return m, fc
}

func invocationStatus(t *testing.T, m *ssm.Mock, commandID, instanceID string) string {
	t.Helper()

	inv, err := m.GetCommandInvocation(context.Background(), commandID, instanceID, "")
	if err != nil {
		t.Fatalf("GetCommandInvocation %s: %v", instanceID, err)
	}

	return inv.Status
}

// MaxConcurrency 1 runs the invocations one after the other.
func TestRunCommandMaxConcurrencyStaggers(t *testing.T) {
	m, fc := newSettledMock(t)

	cmd, err := m.SendCommand(context.Background(), ssmdriver.CommandConfig{
		InstanceIDs: []string{webA, webB}, DocumentName: "AWS-RunShellScript", MaxConcurrency: "1",
		Parameters: map[string][]string{"commands": {"uptime"}},
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	fc.Advance(2 * time.Second)

	if a, b := invocationStatus(t, m, cmd.CommandID, webA), invocationStatus(t, m, cmd.CommandID, webB); a != "InProgress" || b != "Pending" {
		t.Fatalf("first batch: %s / %s, want InProgress / Pending", a, b)
	}

	fc.Advance(5 * time.Second)

	if a, b := invocationStatus(t, m, cmd.CommandID, webA), invocationStatus(t, m, cmd.CommandID, webB); a != "Success" || b != "InProgress" {
		t.Fatalf("second batch: %s / %s, want Success / InProgress", a, b)
	}

	fc.Advance(time.Minute)

	cmds, err := m.ListCommands(context.Background(), ssmdriver.CommandQuery{CommandID: cmd.CommandID})
	if err != nil || len(cmds) != 1 || cmds[0].Status != "Success" || cmds[0].CompletedCount != 2 {
		t.Fatalf("ListCommands = %+v, %v", cmds, err)
	}
}

// A tag target that selects a stopped instance waits for delivery and then
// times out, while the running ones succeed.
func TestRunCommandStoppedTargetTimesOut(t *testing.T) {
	m, fc := newSettledMock(t)

	cmd, err := m.SendCommand(context.Background(), ssmdriver.CommandConfig{
		Targets: []ssmdriver.CommandTarget{{Key: "tag:Role", Values: []string{"web"}}}, DocumentName: "AWS-RunShellScript",
		TimeoutSeconds: 60, Parameters: map[string][]string{"commands": {"uptime"}},
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	if cmd.TargetCount != 3 {
		t.Fatalf("TargetCount = %d, want 3", cmd.TargetCount)
	}

	fc.Advance(30 * time.Second)

	if s := invocationStatus(t, m, cmd.CommandID, webC); s != "Delayed" {
		t.Fatalf("stopped instance before timeout = %s, want Delayed", s)
	}

	fc.Advance(time.Minute)

	inv, _ := m.GetCommandInvocation(context.Background(), cmd.CommandID, webC, "")
	if inv.Status != "TimedOut" || inv.StatusDetails != "DeliveryTimedOut" {
		t.Fatalf("stopped instance after timeout = %s / %s", inv.Status, inv.StatusDetails)
	}

	cmds, _ := m.ListCommands(context.Background(), ssmdriver.CommandQuery{CommandID: cmd.CommandID})
	if c := cmds[0]; c.Status != "Failed" || c.StatusDetails != "Incomplete" || c.DeliveryTimedOutCount != 1 || c.ErrorCount != 0 {
		t.Fatalf("command = %+v, want Failed/Incomplete with one delivery timeout", c)
	}

	byStatus, err := m.ListCommandInvocations(context.Background(), ssmdriver.CommandQuery{
		Filters: []ssmdriver.CommandFilter{{Key: "Status", Value: "DeliveryTimedOut"}},
	}, false)
	if err != nil || len(byStatus) != 1 || byStatus[0].InstanceID != webC {
		t.Fatalf("Status filter = %+v, %v", byStatus, err)
	}
}

// Command history survives a snapshot: a restored mock reports the same
// commands, keeps settling them from their send time and honours cancels.
func TestRunCommandSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, fc := newSettledMock(t)

	done, err := src.SendCommand(ctx, ssmdriver.CommandConfig{
		InstanceIDs: []string{webA}, DocumentName: "AWS-RunShellScript", Comment: "done",
		Parameters: map[string][]string{"commands": {"uptime"}},
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	fc.Advance(time.Minute)

	running, err := src.SendCommand(ctx, ssmdriver.CommandConfig{
		InstanceIDs: []string{webA, webB}, DocumentName: "AWS-RunShellScript",
		Parameters: map[string][]string{"commands": {"sleep 5"}},
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	fc.Advance(2 * time.Second)

	if err := src.CancelCommand(ctx, running.CommandID, []string{webB}); err != nil {
		t.Fatalf("CancelCommand: %v", err)
	}

	raw, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := ssm.New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	cmds, err := dst.ListCommands(ctx, ssmdriver.CommandQuery{})
	if err != nil || len(cmds) != 2 || cmds[0].CommandID != running.CommandID || cmds[1].Comment != "done" {
		t.Fatalf("restored commands = %+v, %v", cmds, err)
	}

	if s := invocationStatus(t, dst, running.CommandID, webA); s != "InProgress" {
		t.Fatalf("restored running invocation = %s, want InProgress", s)
	}

	fc.Advance(time.Minute)

	if a, b := invocationStatus(t, dst, running.CommandID, webA), invocationStatus(t, dst, running.CommandID, webB); a != "Success" || b != "Cancelled" {
		t.Fatalf("restored invocations = %s / %s, want Success / Cancelled", a, b)
	}

	if s := invocationStatus(t, dst, done.CommandID, webA); s != "Success" {
		t.Fatalf("restored finished invocation = %s", s)
	}
}

// A snapshot written before the command history existed still restores its
// invocations as finished commands.
func TestRunCommandRestoresLegacySnapshot(t *testing.T) {
	const legacy = `{"commands":{"11111111-2222-3333-4444-555555555555|i-0aaaaaaaaaaaaaaaa":` +
		`{"CommandID":"11111111-2222-3333-4444-555555555555","InstanceID":"i-0aaaaaaaaaaaaaaaa",` +
		`"DocumentName":"AWS-RunShellScript","Status":"Success","ResponseCode":0}}}`

	m := newMock()
	if err := m.Restore(context.Background(), []byte(legacy)); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	inv, err := m.GetCommandInvocation(context.Background(), "11111111-2222-3333-4444-555555555555", webA, "")
	if err != nil || inv.Status != "Success" || inv.DocumentName != "AWS-RunShellScript" {
		t.Fatalf("legacy invocation = %+v, %v", inv, err)
	}
}
