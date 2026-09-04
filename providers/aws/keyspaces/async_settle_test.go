package keyspaces

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// newAsyncMock builds a Keyspaces mock with AsyncSettle enabled and a FakeClock
// so create/update report their real transient table states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

func tableStatus(t *testing.T, m *Mock, keyspace, table string) string {
	t.Helper()

	got, err := m.GetTable(context.Background(), keyspace, table)
	if err != nil {
		t.Fatalf("get table: %v", err)
	}

	return got.Status
}

// TestAsyncSettleCreateUpdate pins the AsyncSettle transitions: a table reports
// CREATING then ACTIVE on create, and UPDATING then ACTIVE on update.
func TestAsyncSettleCreateUpdate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	created, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t1", SchemaDefinition: sampleSchema(),
	})
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	if created.Status != ksdriver.StatusCreating {
		t.Fatalf("create status = %q, want %q", created.Status, ksdriver.StatusCreating)
	}

	if got := tableStatus(t, m, "app", "t1"); got != ksdriver.StatusCreating {
		t.Fatalf("get status = %q, want %q", got, ksdriver.StatusCreating)
	}

	// List observes the transient state too.
	list, err := m.ListTables(ctx, "app")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}

	if len(list) != 1 || list[0].Status != ksdriver.StatusCreating {
		t.Fatalf("list status = %+v, want one CREATING", list)
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultKeyspacesSettle - time.Millisecond)

	if got := tableStatus(t, m, "app", "t1"); got != ksdriver.StatusCreating {
		t.Fatalf("pre-settle status = %q, want %q", got, ksdriver.StatusCreating)
	}

	// Past the window: terminal ACTIVE.
	fc.Advance(time.Millisecond)

	if got := tableStatus(t, m, "app", "t1"); got != ksdriver.StatusActive {
		t.Fatalf("settled status = %q, want %q", got, ksdriver.StatusActive)
	}

	// Update → UPDATING → ACTIVE.
	updated, err := m.UpdateTable(ctx, ksdriver.UpdateTableConfig{
		KeyspaceName: "app", Name: "t1", Comment: "changed",
	})
	if err != nil {
		t.Fatalf("update table: %v", err)
	}

	if updated.Status != ksdriver.StatusUpdating {
		t.Fatalf("update status = %q, want %q", updated.Status, ksdriver.StatusUpdating)
	}

	if got := tableStatus(t, m, "app", "t1"); got != ksdriver.StatusUpdating {
		t.Fatalf("post-update get status = %q, want %q", got, ksdriver.StatusUpdating)
	}

	fc.Advance(settle.DefaultKeyspacesSettle)

	if got := tableStatus(t, m, "app", "t1"); got != ksdriver.StatusActive {
		t.Fatalf("post-update settled status = %q, want %q", got, ksdriver.StatusActive)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and update are observed as ACTIVE
// immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	created, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t1", SchemaDefinition: sampleSchema(),
	})
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	if created.Status != ksdriver.StatusActive {
		t.Fatalf("create status = %q, want %q", created.Status, ksdriver.StatusActive)
	}

	if got := tableStatus(t, m, "app", "t1"); got != ksdriver.StatusActive {
		t.Fatalf("get status = %q, want %q", got, ksdriver.StatusActive)
	}

	updated, err := m.UpdateTable(ctx, ksdriver.UpdateTableConfig{
		KeyspaceName: "app", Name: "t1", Comment: "changed",
	})
	if err != nil {
		t.Fatalf("update table: %v", err)
	}

	if updated.Status != ksdriver.StatusActive {
		t.Fatalf("update status = %q, want %q", updated.Status, ksdriver.StatusActive)
	}
}
