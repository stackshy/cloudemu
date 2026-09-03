package sql

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// newAsyncMock builds an Azure SQL mock with AsyncSettle enabled and a FakeClock
// so create/modify report their real transient states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// TestAsyncSettleInstanceCreateModify pins the AsyncSettle transitions on the
// portable relationaldb Instance path: a database reports creating on create
// and modifying on modify, then settles to available, all driven by the
// FakeClock. A stop transition clears any pending create window.
func TestAsyncSettleInstanceCreateModify(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", ClusterID: "srv1"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if created.State != rdsdriver.StateCreating {
		t.Fatalf("create response State = %q, want creating", created.State)
	}

	got, err := m.DescribeInstances(ctx, []string{"srv1/db1"})
	if err != nil || got[0].State != rdsdriver.StateCreating {
		t.Fatalf("describe before settle = %q (err %v), want creating", got[0].State, err)
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultAzureDBSettle - time.Millisecond)

	got, _ = m.DescribeInstances(ctx, []string{"srv1/db1"})
	if got[0].State != rdsdriver.StateCreating {
		t.Fatalf("describe just before settle = %q, want creating", got[0].State)
	}

	// Past the window: terminal available.
	fc.Advance(time.Millisecond)

	got, _ = m.DescribeInstances(ctx, []string{"srv1/db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after settle = %q, want available", got[0].State)
	}

	// Modify → modifying → available.
	modified, err := m.ModifyInstance(ctx, "srv1/db1", rdsdriver.ModifyInstanceInput{InstanceClass: "GP_Gen5_4"})
	if err != nil || modified.State != rdsdriver.StateModifying {
		t.Fatalf("modify response State = %q (err %v), want modifying", modified.State, err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"srv1/db1"})
	if got[0].State != rdsdriver.StateModifying {
		t.Fatalf("describe during modify = %q, want modifying", got[0].State)
	}

	fc.Advance(settle.DefaultAzureDBSettle)

	got, _ = m.DescribeInstances(ctx, []string{"srv1/db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe after modify settle = %q, want available", got[0].State)
	}

	// A stop transition clears the settle window immediately.
	fresh, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db2", ClusterID: "srv1"})
	if err != nil || fresh.State != rdsdriver.StateCreating {
		t.Fatalf("CreateInstance db2 = %q (err %v), want creating", fresh.State, err)
	}

	if err := m.StopInstance(ctx, "srv1/db2"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"srv1/db2"})
	if got[0].State != rdsdriver.StateStopped {
		t.Fatalf("stopped db2 = %q, want stopped (window must be cleared)", got[0].State)
	}
}

// TestAsyncSettleDatabaseStatus pins the AsyncSettle transitions on the wire
// Databases-capability path: a database reports status Creating on create and
// Scaling on update, then settles to "" (the wire reports terminal Online).
func TestAsyncSettleDatabaseStatus(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if s := m.DatabaseTransientStatus("srv1", "app"); s != dbStatusCreating {
		t.Fatalf("status before settle = %q, want Creating", s)
	}

	fc.Advance(settle.DefaultAzureDBSettle - time.Millisecond)

	if s := m.DatabaseTransientStatus("srv1", "app"); s != dbStatusCreating {
		t.Fatalf("status just before settle = %q, want Creating", s)
	}

	fc.Advance(time.Millisecond)

	if s := m.DatabaseTransientStatus("srv1", "app"); s != "" {
		t.Fatalf("status after settle = %q, want empty (Online)", s)
	}

	// Update → Scaling → "".
	if _, err := m.UpdateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "app", SKUName: "GP_Gen5_4"}); err != nil {
		t.Fatalf("UpdateDatabase: %v", err)
	}

	if s := m.DatabaseTransientStatus("srv1", "app"); s != dbStatusScaling {
		t.Fatalf("status during update = %q, want Scaling", s)
	}

	fc.Advance(settle.DefaultAzureDBSettle)

	if s := m.DatabaseTransientStatus("srv1", "app"); s != "" {
		t.Fatalf("status after update settle = %q, want empty (Online)", s)
	}

	// Delete clears the window.
	if err := m.DeleteDatabase(ctx, "srv1", "app"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if s := m.DatabaseTransientStatus("srv1", "app"); s != "" {
		t.Fatalf("status after delete = %q, want empty", s)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and modify are observed as available (and a
// database's status stays terminal Online, i.e. empty transient) immediately.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", ClusterID: "srv1"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if created.State != rdsdriver.StateAvailable {
		t.Fatalf("create response State = %q, want available (settle off)", created.State)
	}

	got, _ := m.DescribeInstances(ctx, []string{"srv1/db1"})
	if got[0].State != rdsdriver.StateAvailable {
		t.Fatalf("describe = %q, want available (settle off)", got[0].State)
	}

	modified, err := m.ModifyInstance(ctx, "srv1/db1", rdsdriver.ModifyInstanceInput{InstanceClass: "GP_Gen5_4"})
	if err != nil || modified.State != rdsdriver.StateAvailable {
		t.Fatalf("modify response State = %q (err %v), want available (settle off)", modified.State, err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv1", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if s := m.DatabaseTransientStatus("srv1", "app"); s != "" {
		t.Fatalf("database status = %q, want empty/Online (settle off)", s)
	}
}
