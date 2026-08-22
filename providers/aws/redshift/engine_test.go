package redshift

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// recordingEngine is a stub config.DatabaseEngine that records calls and returns
// a fixed address, so the wiring can be tested without a real database.
type recordingEngine struct {
	provisioned   []config.ProvisionRequest
	deprovisioned []string
	host          string
	port          int
}

func (e *recordingEngine) Provision(_ context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	e.provisioned = append(e.provisioned, req)

	return config.ProvisionResult{Host: e.host, Port: e.port}, nil
}

func (e *recordingEngine) Deprovision(_ context.Context, id string) error {
	e.deprovisioned = append(e.deprovisioned, id)

	return nil
}

func newEngineMock(eng config.DatabaseEngine) *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"), config.WithDatabaseEngine(eng),
	)

	return New(opts)
}

// TestCreateClusterUsesEngineEndpoint proves the engine host:port is written back
// onto the cluster endpoint (Redshift routes to the Postgres engine), and that a
// delete deprovisions.
func TestCreateClusterUsesEngineEndpoint(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := newEngineMock(eng)
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID: "warehouse", MasterUsername: "admin", MasterUserPassword: "secret", DatabaseName: "app",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if c.Endpoint != "127.0.0.1" || c.Port != 55432 {
		t.Fatalf("endpoint not overridden by engine: %s:%d", c.Endpoint, c.Port)
	}

	got, err := m.DescribeClusters(ctx, []string{"warehouse"})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeClusters: %v (%d)", err, len(got))
	}

	if got[0].Endpoint != "127.0.0.1" || got[0].Port != 55432 {
		t.Fatalf("stored cluster endpoint not backed by the engine: %s:%d", got[0].Endpoint, got[0].Port)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Username != "admin" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteCluster(ctx, "warehouse"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "warehouse" {
		t.Fatalf("expected one deprovision for warehouse, got %v", eng.deprovisioned)
	}
}

// failingEngine fails every Provision, to exercise the create rollback.
type failingEngine struct{}

func (failingEngine) Provision(_ context.Context, _ config.ProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{}, context.DeadlineExceeded
}

func (failingEngine) Deprovision(_ context.Context, _ string) error { return nil }

// TestCreateClusterProvisionFailureRollsBack proves a failed provision leaves no
// reserved cluster behind.
func TestCreateClusterProvisionFailureRollsBack(t *testing.T) {
	m := newEngineMock(failingEngine{})
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse"}); err == nil {
		t.Fatal("expected CreateCluster to fail when the engine provision fails")
	}

	got, err := m.DescribeClusters(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("failed provision must leave no reserved cluster, got %d", len(got))
	}
}

// blockingEngine is a config.DatabaseEngine whose Provision blocks until release
// is signalled, announcing (once) when it has been entered.
type blockingEngine struct {
	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
}

func newBlockingEngine() *blockingEngine {
	return &blockingEngine{entered: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingEngine) Provision(_ context.Context, _ config.ProvisionRequest) (config.ProvisionResult, error) {
	e.enterOnce.Do(func() { close(e.entered) })
	<-e.release

	return config.ProvisionResult{Host: "127.0.0.1", Port: 55432}, nil
}

func (e *blockingEngine) Deprovision(_ context.Context, _ string) error { return nil }

// TestCreateClusterReleasesLockDuringProvision proves the provider lock is
// released around the (potentially slow) engine provision: a concurrent
// DescribeClusters must return while a provision is still in flight.
func TestCreateClusterReleasesLockDuringProvision(t *testing.T) {
	eng := newBlockingEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "warehouse"})
		created <- err
	}()

	select {
	case <-eng.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provision never started")
	}

	describeReturned := make(chan struct{})

	go func() {
		_, _ = m.DescribeClusters(ctx, nil)
		close(describeReturned)
	}()

	select {
	case <-describeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("DescribeClusters blocked while a provision held the provider lock")
	}

	close(eng.release)

	if err := <-created; err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeClusters(ctx, []string{"warehouse"})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeClusters: %v (%d)", err, len(got))
	}

	if got[0].Endpoint != "127.0.0.1" || got[0].Port != 55432 {
		t.Fatalf("finalized endpoint not written back: %s:%d", got[0].Endpoint, got[0].Port)
	}
}
