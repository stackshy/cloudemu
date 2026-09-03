package memorydb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// newAsyncMock builds a MemoryDB mock with AsyncSettle enabled and a FakeClock so
// create/update report their real transient states deterministically.
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

func clusterStatus(t *testing.T, m *Mock, name string) string {
	t.Helper()

	got, err := m.DescribeClusters(context.Background(), []string{name})
	if err != nil {
		t.Fatalf("describe cluster: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("describe returned %d clusters, want 1", len(got))
	}

	return got[0].Status
}

// TestAsyncSettleCreateUpdate pins the AsyncSettle transitions: a cluster reports
// creating then available on create, and updating then available on update.
func TestAsyncSettleCreateUpdate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", NumShards: 1})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if created.Status != mdbdriver.StatusCreating {
		t.Fatalf("create status = %q, want %q", created.Status, mdbdriver.StatusCreating)
	}

	if got := clusterStatus(t, m, "c1"); got != mdbdriver.StatusCreating {
		t.Fatalf("describe status = %q, want %q", got, mdbdriver.StatusCreating)
	}

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultClusterSettle - time.Millisecond)

	if got := clusterStatus(t, m, "c1"); got != mdbdriver.StatusCreating {
		t.Fatalf("pre-settle status = %q, want %q", got, mdbdriver.StatusCreating)
	}

	// Past the window: terminal available.
	fc.Advance(time.Millisecond)

	if got := clusterStatus(t, m, "c1"); got != mdbdriver.StatusAvailable {
		t.Fatalf("settled status = %q, want %q", got, mdbdriver.StatusAvailable)
	}

	// Update → updating → available.
	newNode := "db.r6g.large"
	updated, err := m.UpdateCluster(ctx, mdbdriver.UpdateClusterConfig{Name: "c1", NodeType: newNode})
	if err != nil {
		t.Fatalf("update cluster: %v", err)
	}

	if updated.Status != mdbdriver.StatusUpdating {
		t.Fatalf("update status = %q, want %q", updated.Status, mdbdriver.StatusUpdating)
	}

	if got := clusterStatus(t, m, "c1"); got != mdbdriver.StatusUpdating {
		t.Fatalf("post-update describe status = %q, want %q", got, mdbdriver.StatusUpdating)
	}

	fc.Advance(settle.DefaultCacheModifySettle)

	if got := clusterStatus(t, m, "c1"); got != mdbdriver.StatusAvailable {
		t.Fatalf("post-update settled status = %q, want %q", got, mdbdriver.StatusAvailable)
	}
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and update are observed as available
// immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", NumShards: 1})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if created.Status != mdbdriver.StatusAvailable {
		t.Fatalf("create status = %q, want %q", created.Status, mdbdriver.StatusAvailable)
	}

	if got := clusterStatus(t, m, "c1"); got != mdbdriver.StatusAvailable {
		t.Fatalf("describe status = %q, want %q", got, mdbdriver.StatusAvailable)
	}

	updated, err := m.UpdateCluster(ctx, mdbdriver.UpdateClusterConfig{Name: "c1", NodeType: "db.r6g.large"})
	if err != nil {
		t.Fatalf("update cluster: %v", err)
	}

	if updated.Status != mdbdriver.StatusAvailable {
		t.Fatalf("update status = %q, want %q", updated.Status, mdbdriver.StatusAvailable)
	}
}
