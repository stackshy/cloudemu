package dynamodb

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// TestKinesisStreamingDestinationLifecycle covers enable/describe/update/disable
// and the not-found guards for a missing table and a missing destination.
func TestKinesisStreamingDestinationLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "events")

	const arn = "arn:aws:kinesis:us-east-1:000000000000:stream/s"

	dest, err := m.EnableKinesisStreamingDestination(ctx, "events", arn, "MILLISECOND")
	requireNoError(t, err)
	assertEqual(t, driver.KinesisStatusActive, dest.Status)
	assertEqual(t, "MILLISECOND", dest.Precision)

	updated, err := m.UpdateKinesisStreamingDestination(ctx, "events", arn, "MICROSECOND")
	requireNoError(t, err)
	assertEqual(t, "MICROSECOND", updated.Precision)

	dests, err := m.DescribeKinesisStreamingDestination(ctx, "events")
	requireNoError(t, err)
	assertEqual(t, 1, len(dests))
	assertEqual(t, "MICROSECOND", dests[0].Precision)

	disabled, err := m.DisableKinesisStreamingDestination(ctx, "events", arn)
	requireNoError(t, err)
	assertEqual(t, driver.KinesisStatusDisabled, disabled.Status)

	_, err = m.EnableKinesisStreamingDestination(ctx, "ghost", arn, "")
	assertTrueT(t, cerrors.IsNotFound(err), "enable on missing table should be NotFound")

	_, err = m.DisableKinesisStreamingDestination(ctx, "events", "arn:aws:kinesis:us-east-1:0:stream/other")
	assertTrueT(t, cerrors.IsNotFound(err), "disable of missing destination should be NotFound")
}

// TestContributorInsightsLifecycle covers enable/describe/list/disable plus the
// missing-index and missing-table guards.
func TestContributorInsightsLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "metrics")

	status, err := m.UpdateContributorInsights(ctx, "metrics", "", true)
	requireNoError(t, err)
	assertEqual(t, driver.ContributorInsightsEnabled, status)

	got, lastUpdate, err := m.DescribeContributorInsights(ctx, "metrics", "")
	requireNoError(t, err)
	assertEqual(t, driver.ContributorInsightsEnabled, got)
	assertTrueT(t, lastUpdate > 0, "enabled record should carry a last-update time")

	summaries, err := m.ListContributorInsights(ctx, "")
	requireNoError(t, err)
	assertEqual(t, 1, len(summaries))
	assertEqual(t, "metrics", summaries[0].Table)

	status, err = m.UpdateContributorInsights(ctx, "metrics", "", false)
	requireNoError(t, err)
	assertEqual(t, driver.ContributorInsightsDisabled, status)

	// A never-touched table reads DISABLED, not an error.
	createTestTable(m, "fresh")
	got, _, err = m.DescribeContributorInsights(ctx, "fresh", "")
	requireNoError(t, err)
	assertEqual(t, driver.ContributorInsightsDisabled, got)

	_, err = m.UpdateContributorInsights(ctx, "metrics", "no-index", true)
	assertTrueT(t, cerrors.IsNotFound(err), "unknown index should be NotFound")

	_, _, err = m.DescribeContributorInsights(ctx, "ghost", "")
	assertTrueT(t, cerrors.IsNotFound(err), "missing table should be NotFound")
}

// TestGlobalTableLifecycle covers create/describe/list/update and the guards for
// a missing underlying table, a duplicate global table, a missing global table,
// a duplicate replica and a missing replica.
func TestGlobalTableLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "sessions")

	info, err := m.CreateGlobalTable(ctx, "sessions", []string{"us-east-1"})
	requireNoError(t, err)
	assertEqual(t, driver.GlobalTableStatusActive, info.Status)
	assertEqual(t, 1, len(info.Regions))
	assertNotEmpty(t, info.Arn)

	desc, err := m.DescribeGlobalTable(ctx, "sessions")
	requireNoError(t, err)
	assertEqual(t, "sessions", desc.Name)

	all, err := m.ListGlobalTables(ctx, "")
	requireNoError(t, err)
	assertEqual(t, 1, len(all))

	upd, err := m.UpdateGlobalTable(ctx, "sessions", []string{"us-west-2"}, nil)
	requireNoError(t, err)
	assertEqual(t, 2, len(upd.Regions))

	rem, err := m.UpdateGlobalTable(ctx, "sessions", nil, []string{"us-west-2"})
	requireNoError(t, err)
	assertEqual(t, 1, len(rem.Regions))

	filtered, err := m.ListGlobalTables(ctx, "eu-west-1")
	requireNoError(t, err)
	assertEqual(t, 0, len(filtered))

	_, err = m.CreateGlobalTable(ctx, "ghost", []string{"us-east-1"})
	assertTrueT(t, cerrors.IsNotFound(err), "create over missing table should be NotFound")

	_, err = m.CreateGlobalTable(ctx, "sessions", []string{"us-east-1"})
	assertTrueT(t, cerrors.IsAlreadyExists(err), "duplicate global table should be AlreadyExists")

	_, err = m.DescribeGlobalTable(ctx, "nope")
	assertTrueT(t, cerrors.IsNotFound(err), "describe of missing global table should be NotFound")

	_, err = m.UpdateGlobalTable(ctx, "sessions", []string{"us-east-1"}, nil)
	assertTrueT(t, cerrors.IsAlreadyExists(err), "duplicate replica should be AlreadyExists")

	_, err = m.UpdateGlobalTable(ctx, "sessions", nil, []string{"ap-south-1"})
	assertTrueT(t, cerrors.IsFailedPrecondition(err), "missing replica should be FailedPrecondition")
}

// TestOpSurfaceSnapshotRoundTrip verifies the new state (Kinesis destinations,
// Contributor Insights records, global tables) survives a Snapshot/Restore.
func TestOpSurfaceSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	createTestTable(m, "orders")

	const arn = "arn:aws:kinesis:us-east-1:000000000000:stream/s"
	requireNoError(t, mustDest(m.EnableKinesisStreamingDestination(ctx, "orders", arn, "MILLISECOND")))
	requireNoError(t, mustStatus(m.UpdateContributorInsights(ctx, "orders", "", true)))
	_, err := m.CreateGlobalTable(ctx, "orders", []string{"us-east-1", "us-west-2"})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, true)
	requireNoError(t, err)

	restored := newTestMock()
	requireNoError(t, restored.Restore(ctx, data))

	dests, err := restored.DescribeKinesisStreamingDestination(ctx, "orders")
	requireNoError(t, err)
	assertEqual(t, 1, len(dests))
	assertEqual(t, "MILLISECOND", dests[0].Precision)

	status, _, err := restored.DescribeContributorInsights(ctx, "orders", "")
	requireNoError(t, err)
	assertEqual(t, driver.ContributorInsightsEnabled, status)

	gt, err := restored.DescribeGlobalTable(ctx, "orders")
	requireNoError(t, err)
	assertEqual(t, 2, len(gt.Regions))
}

func mustDest(_ driver.KinesisDestination, err error) error { return err }
func mustStatus(_ string, err error) error                  { return err }

func assertTrueT(t *testing.T, cond bool, msg string) {
	t.Helper()

	if !cond {
		t.Fatalf("expected true: %s", msg)
	}
}
