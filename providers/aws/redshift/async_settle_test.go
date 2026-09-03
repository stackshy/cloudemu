package redshift

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// newAsyncMock builds a Redshift mock with AsyncSettle enabled and a FakeClock so
// create/modify report their real transient states deterministically.
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

func clusterState(t *testing.T, m *Mock, id string) string {
	t.Helper()

	got, err := m.DescribeClusters(context.Background(), []string{id})
	if err != nil {
		t.Fatalf("describe cluster: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("describe returned %d clusters, want 1", len(got))
	}

	return got[0].State
}

// TestAsyncSettleCreateModify pins the AsyncSettle transitions: a cluster reports
// creating then available on create, and modifying then available on modify.
func TestAsyncSettleCreateModify(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if created.State != rdbdriver.StateCreating {
		t.Fatalf("create state = %q, want %q", created.State, rdbdriver.StateCreating)
	}

	if got := clusterState(t, m, "c1"); got != rdbdriver.StateCreating {
		t.Fatalf("describe state = %q, want %q", got, rdbdriver.StateCreating)
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultClusterSettle - time.Millisecond)

	if got := clusterState(t, m, "c1"); got != rdbdriver.StateCreating {
		t.Fatalf("pre-settle state = %q, want %q", got, rdbdriver.StateCreating)
	}

	// Past the window: terminal available.
	fc.Advance(time.Millisecond)

	if got := clusterState(t, m, "c1"); got != rdbdriver.StateAvailable {
		t.Fatalf("settled state = %q, want %q", got, rdbdriver.StateAvailable)
	}

	// Modify → modifying → available.
	modified, err := m.ModifyCluster(ctx, "c1", rdbdriver.ModifyInstanceInput{NodeType: "dc2.large"})
	if err != nil {
		t.Fatalf("modify cluster: %v", err)
	}

	if modified.State != rdbdriver.StateModifying {
		t.Fatalf("modify state = %q, want %q", modified.State, rdbdriver.StateModifying)
	}

	if got := clusterState(t, m, "c1"); got != rdbdriver.StateModifying {
		t.Fatalf("post-modify describe state = %q, want %q", got, rdbdriver.StateModifying)
	}

	fc.Advance(settle.DefaultWarehouseResize)

	if got := clusterState(t, m, "c1"); got != rdbdriver.StateAvailable {
		t.Fatalf("post-modify settled state = %q, want %q", got, rdbdriver.StateAvailable)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and modify are observed as available
// immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "c1"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if created.State != rdbdriver.StateAvailable {
		t.Fatalf("create state = %q, want %q", created.State, rdbdriver.StateAvailable)
	}

	if got := clusterState(t, m, "c1"); got != rdbdriver.StateAvailable {
		t.Fatalf("describe state = %q, want %q", got, rdbdriver.StateAvailable)
	}

	modified, err := m.ModifyCluster(ctx, "c1", rdbdriver.ModifyInstanceInput{NodeType: "dc2.large"})
	if err != nil {
		t.Fatalf("modify cluster: %v", err)
	}

	if modified.State != rdbdriver.StateAvailable {
		t.Fatalf("modify state = %q, want %q", modified.State, rdbdriver.StateAvailable)
	}
}
