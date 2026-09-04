package elbv2

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// seedELB populates a mock across all of its serialized state — load balancers,
// target groups, listeners, rules, per-target health, and both attribute maps —
// so a round-trip has to carry every one of them. It returns ids a caller
// asserts on after a restore.
func seedELB(t *testing.T, m *Mock) (lbARN, tgARN string) {
	t.Helper()
	ctx := context.Background()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "web-lb", Type: "application"})
	requireNoError(t, err)
	lbARN = lb.ARN

	tg, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{
		Name: "web-tg", Protocol: "HTTP", Port: 80, TargetType: "instance",
	})
	requireNoError(t, err)
	tgARN = tg.ARN

	_, err = m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lbARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tgARN,
	})
	requireNoError(t, err)

	requireNoError(t, m.RegisterTargets(ctx, tgARN, []driver.Target{
		{ID: "i-aaaa", Port: 80}, {ID: "i-bbbb", Port: 8080},
	}))

	requireNoError(t, m.PutLBAttributes(ctx, lbARN, driver.LBAttributes{DeletionProtection: true}))

	_, err = m.ModifyTargetGroupAttributes(ctx, tgARN, map[string]string{"deregistration_delay.timeout_seconds": "60"})
	requireNoError(t, err)

	return lbARN, tgARN
}

// TestSnapshotRestoreRoundTrip proves the ELBv2 mock serializes its entire state
// and restores it into a fresh mock identity-preservingly: re-snapshotting the
// restored mock yields byte-identical JSON, and the seeded resources come back
// under their original ARNs.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	lbARN, tgARN := seedELB(t, src)

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	lbs, err := dst.DescribeLoadBalancers(ctx, []string{lbARN})
	requireNoError(t, err)
	if len(lbs) != 1 || lbs[0].ARN != lbARN {
		t.Fatalf("restored LBs = %v, want ARN %q", lbs, lbARN)
	}

	health, err := dst.DescribeTargetHealth(ctx, tgARN)
	requireNoError(t, err)
	if len(health) != 2 {
		t.Fatalf("restored %d target-health entries, want 2", len(health))
	}

	attrs, err := dst.GetLBAttributes(ctx, lbARN)
	requireNoError(t, err)
	if attrs == nil || !attrs.DeletionProtection {
		t.Fatalf("restored LB attributes = %v, want DeletionProtection=true", attrs)
	}

	tgAttrs, err := dst.GetTargetGroupAttributes(ctx, tgARN)
	requireNoError(t, err)
	if tgAttrs["deregistration_delay.timeout_seconds"] != "60" {
		t.Fatalf("restored TG attributes = %v, want deregistration_delay=60", tgAttrs)
	}
}

// TestSnapshotRestoreRoundTripListenerAttrsAndTags proves listener attribute
// overrides and listener/rule tags — both added after the original snapshot
// shape was fixed — survive a snapshot/restore round-trip too, not just the
// four fields covered by seedELB above.
func TestSnapshotRestoreRoundTripListenerAttrsAndTags(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	lb, err := src.CreateLoadBalancer(ctx, driver.LBConfig{Name: "attr-lb", Type: "network"})
	requireNoError(t, err)

	li, err := src.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "TCP", Port: 80, Tags: map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	rule, err := src.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: li.ARN,
		Priority:    1,
		Conditions:  []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/x"}}},
		Tags:        map[string]string{"team": "platform"},
	})
	requireNoError(t, err)

	_, err = src.ModifyListenerAttributes(ctx, li.ARN, map[string]string{"tcp.idle_timeout.seconds": "60"})
	requireNoError(t, err)

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	gotListener, err := dst.GetListener(ctx, li.ARN)
	requireNoError(t, err)
	assertEqual(t, "prod", gotListener.Tags["env"])

	gotRule, err := dst.GetRule(ctx, rule.ARN)
	requireNoError(t, err)
	assertEqual(t, "platform", gotRule.Tags["team"])

	gotAttrs, err := dst.GetListenerAttributes(ctx, li.ARN)
	requireNoError(t, err)
	assertEqual(t, "60", gotAttrs["tcp.idle_timeout.seconds"])
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("empty snapshot not stable: %s vs %s", data, data2)
	}
}
