package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// settleEpoch is a fixed base time so the FakeClock-driven settle assertions are
// deterministic.
var settleEpoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test fixture

func newSettleMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(settleEpoch)

	return New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle())), fc
}

// TestTableStatusCreatingToActive verifies a created table reports CREATING
// until its settle window elapses, then ACTIVE, when async settling is on.
func TestTableStatusCreatingToActive(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk"}))

	assertEqual(t, statusCreating, m.TableStatus("t1"))

	fc.Advance(settleTableCreate - time.Millisecond)
	assertEqual(t, statusCreating, m.TableStatus("t1"))

	fc.Advance(time.Millisecond)
	assertEqual(t, statusActive, m.TableStatus("t1"))
}

// TestTableStatusUpdatingToActive verifies UpdateThroughput drives the table
// through UPDATING back to ACTIVE.
func TestTableStatusUpdatingToActive(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk"}))
	fc.Advance(settleTableCreate)
	assertEqual(t, statusActive, m.TableStatus("t1"))

	requireNoError(t, m.UpdateThroughput(ctx, "t1", "PROVISIONED", 10, 10))
	assertEqual(t, statusUpdating, m.TableStatus("t1"))

	fc.Advance(settleTableUpdate)
	assertEqual(t, statusActive, m.TableStatus("t1"))
}

// TestGSIStatusCreatingToActive verifies a GSI added via CreateIndex reports
// CREATING until its window elapses, then ACTIVE — independent of the table.
func TestGSIStatusCreatingToActive(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t1", PartitionKey: "pk"}))
	fc.Advance(settleTableCreate)

	info, err := m.CreateIndex(ctx, "t1", driver.GSIConfig{Name: "gsi1", PartitionKey: "gk"})
	requireNoError(t, err)
	assertEqual(t, statusCreating, info.Status)
	assertEqual(t, statusCreating, m.GSIStatus("t1", "gsi1"))
	// The table itself stays ACTIVE while its index back-fills.
	assertEqual(t, statusActive, m.TableStatus("t1"))

	fc.Advance(settleGSICreate)
	assertEqual(t, statusActive, m.GSIStatus("t1", "gsi1"))

	desc, err := m.DescribeIndex(ctx, "t1", "gsi1")
	requireNoError(t, err)
	assertEqual(t, statusActive, desc.Status)
}

// TestCreateTimeGSIStatusCreating verifies a GSI declared at CreateTable time
// also settles from CREATING to ACTIVE.
func TestCreateTimeGSIStatusCreating(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "t1", PartitionKey: "pk",
		GSIs: []driver.GSIConfig{{Name: "gsi1", PartitionKey: "gk"}},
	}))

	assertEqual(t, statusCreating, m.GSIStatus("t1", "gsi1"))

	fc.Advance(settleGSICreate)
	assertEqual(t, statusActive, m.GSIStatus("t1", "gsi1"))
}

// TestSettleWindowsClearedOnDelete verifies deleting a table drops its table and
// GSI windows so a re-created same-named table starts a fresh window.
func TestSettleWindowsClearedOnDelete(t *testing.T) {
	m, _ := newSettleMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "t1", PartitionKey: "pk",
		GSIs: []driver.GSIConfig{{Name: "gsi1", PartitionKey: "gk"}},
	}))
	requireNoError(t, m.DeleteTable(ctx, "t1"))

	// With the windows cleared, the absent table/GSI both fall back to ACTIVE.
	assertEqual(t, statusActive, m.TableStatus("t1"))
	assertEqual(t, statusActive, m.GSIStatus("t1", "gsi1"))
}

// TestAsyncSettleDefaultOff guards the blast radius: with the default options
// (AsyncSettle off) a table and its GSI are ACTIVE the instant they are created,
// exactly as before.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "t1", PartitionKey: "pk",
		GSIs: []driver.GSIConfig{{Name: "gsi1", PartitionKey: "gk"}},
	}))

	assertEqual(t, statusActive, m.TableStatus("t1"))
	assertEqual(t, statusActive, m.GSIStatus("t1", "gsi1"))

	info, err := m.CreateIndex(ctx, "t1", driver.GSIConfig{Name: "gsi2", PartitionKey: "gk2"})
	requireNoError(t, err)
	assertEqual(t, statusActive, info.Status)
	assertEqual(t, statusActive, m.GSIStatus("t1", "gsi2"))

	requireNoError(t, m.UpdateThroughput(ctx, "t1", "PROVISIONED", 5, 5))
	assertEqual(t, statusActive, m.TableStatus("t1"))
}
