package mysqlflex

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// newAsyncMock builds a MySQL Flexible Server mock with AsyncSettle enabled and
// a FakeClock so create/modify report their real transient states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
		config.WithAccountID("sub-1"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// TestAsyncSettleCreateModify pins the AsyncSettle transitions: a server reports
// creating (→ wire Starting) then available (→ Ready) on create, modifying (→
// wire Updating) then available on modify — all driven by the FakeClock. A stop
// transition clears any pending create window.
func TestAsyncSettleCreateModify(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv1"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if created.State != rdsdriver.StateCreating {
		t.Fatalf("create response State = %q, want creating", created.State)
	}

	got, err := m.DescribeInstances(ctx, []string{"srv1"})
	if err != nil || got[0].State != rdsdriver.StateCreating {
		t.Fatalf("describe before settle = %q (err %v), want creating", got[0].State, err)
	}

	fc.Advance(settle.DefaultAzureDBSettle - time.Millisecond)

	got, _ = m.DescribeInstances(ctx, []string{"srv1"})
	if got[0].State != rdsdriver.StateCreating {
		t.Fatalf("describe just before settle = %q, want creating", got[0].State)
	}

	fc.Advance(time.Millisecond)

	got, _ = m.DescribeInstances(ctx, []string{"srv1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after settle = %q, want available", got[0].State)
	}

	// Modify → modifying → available.
	modified, err := m.ModifyInstance(ctx, "srv1", rdsdriver.ModifyInstanceInput{InstanceClass: "Standard_D2ds_v4"})
	if err != nil || modified.State != rdsdriver.StateModifying {
		t.Fatalf("modify response State = %q (err %v), want modifying", modified.State, err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"srv1"})
	if got[0].State != rdsdriver.StateModifying {
		t.Fatalf("describe during modify = %q, want modifying", got[0].State)
	}

	fc.Advance(settle.DefaultAzureDBSettle)

	got, _ = m.DescribeInstances(ctx, []string{"srv1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after modify settle = %q, want available", got[0].State)
	}

	// A stop transition clears the create window immediately.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv2"}); err != nil {
		t.Fatalf("CreateInstance srv2: %v", err)
	}

	if err := m.StopInstance(ctx, "srv2"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"srv2"})
	if got[0].State != rdsdriver.StateStopped {
		t.Fatalf("stopped srv2 = %q, want stopped (window must be cleared)", got[0].State)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and modify are observed as available
// immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv1"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if created.State != rdsdriver.StateAvailable {
		t.Fatalf("create response State = %q, want available (settle off)", created.State)
	}

	got, _ := m.DescribeInstances(ctx, []string{"srv1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe = %q, want available (settle off)", got[0].State)
	}

	modified, err := m.ModifyInstance(ctx, "srv1", rdsdriver.ModifyInstanceInput{InstanceClass: "Standard_D2ds_v4"})
	if err != nil || modified.State != rdsdriver.StateAvailable {
		t.Fatalf("modify response State = %q (err %v), want available (settle off)", modified.State, err)
	}
}
