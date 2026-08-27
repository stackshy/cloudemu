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
