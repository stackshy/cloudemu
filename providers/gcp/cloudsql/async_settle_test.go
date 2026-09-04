package cloudsql

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// newAsyncMock builds a Cloud SQL mock with AsyncSettle enabled and a FakeClock
// so create/modify/restart report their real transient GCP states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("mock-project"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// TestAsyncSettleCreateModifyRestart pins the AsyncSettle transitions: an
// instance reports creating (→ wire PENDING_CREATE) then available (→ RUNNABLE)
// on create, modifying (→ MAINTENANCE) then available on patch, and rebooting
// (→ MAINTENANCE) then available on restart — all driven by the FakeClock. A
// start/stop transition clears any pending create window.
func TestAsyncSettleCreateModifyRestart(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "MYSQL_8_0"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if created.State != rdsdriver.StateCreating {
		t.Fatalf("create response State = %q, want creating", created.State)
	}

	got, err := m.DescribeInstances(ctx, []string{"db1"})
	if err != nil || got[0].State != rdsdriver.StateCreating {
		t.Fatalf("describe before settle = %q (err %v), want creating", got[0].State, err)
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultCloudSQLSettle - time.Millisecond)

	got, _ = m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateCreating {
		t.Fatalf("describe just before settle = %q, want creating", got[0].State)
	}

	// Past the window: terminal RUNNABLE.
	fc.Advance(time.Millisecond)

	got, _ = m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after settle = %q, want available", got[0].State)
	}

	// Modify → modifying (MAINTENANCE) → available.
	modified, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{InstanceClass: "db-n1-standard-2"})
	if err != nil || modified.State != rdsdriver.StateModifying {
		t.Fatalf("modify response State = %q (err %v), want modifying", modified.State, err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateModifying {
		t.Fatalf("describe during modify = %q, want modifying", got[0].State)
	}

	fc.Advance(settle.DefaultCloudSQLSettle)

	got, _ = m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after modify settle = %q, want available", got[0].State)
	}

	// Restart → rebooting (MAINTENANCE) → available.
	if err := m.RebootInstance(ctx, "db1"); err != nil {
		t.Fatalf("RebootInstance: %v", err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateRebooting {
		t.Fatalf("describe during restart = %q, want rebooting", got[0].State)
	}

	fc.Advance(settle.DefaultDBRebootSettle)

	got, _ = m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after restart settle = %q, want available", got[0].State)
	}

	// A stop transition clears the create window: a fresh instance stopped
	// mid-settle reports stopped, not a stale creating.
	fresh, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db2", Engine: "MYSQL_8_0"})
	if err != nil || fresh.State != rdsdriver.StateCreating {
		t.Fatalf("CreateInstance db2 = %q (err %v), want creating", fresh.State, err)
	}

	if err := m.StopInstance(ctx, "db2"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"db2"})
	if got[0].State != rdsdriver.StateStopped {
		t.Fatalf("stopped db2 = %q, want stopped (window must be cleared)", got[0].State)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and modify are observed as available (→
// RUNNABLE) immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "MYSQL_8_0"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if created.State != rdsdriver.StateAvailable {
		t.Fatalf("create response State = %q, want available (settle off)", created.State)
	}

	got, _ := m.DescribeInstances(ctx, []string{"db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe = %q, want available (settle off)", got[0].State)
	}

	modified, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{InstanceClass: "db-n1-standard-2"})
	if err != nil || modified.State != rdsdriver.StateAvailable {
		t.Fatalf("modify response State = %q (err %v), want available (settle off)", modified.State, err)
	}
}
