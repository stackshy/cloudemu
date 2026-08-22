package memorydb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// recordingCacheEngine is a stub config.CacheEngine that records calls and hands
// back a fixed address, so the wiring can be tested without a real Redis.
type recordingCacheEngine struct {
	provisioned   []string
	deprovisioned []string
	host          string
	port          int
}

func (e *recordingCacheEngine) Provision(_ context.Context, req config.CacheProvisionRequest) (config.ProvisionResult, error) {
	e.provisioned = append(e.provisioned, req.CacheID)

	return config.ProvisionResult{Host: e.host, Port: e.port}, nil
}

func (e *recordingCacheEngine) Deprovision(_ context.Context, id string) error {
	e.deprovisioned = append(e.deprovisioned, id)

	return nil
}

func newEngineMock(eng config.CacheEngine) *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"), config.WithCacheEngine(eng),
	)

	return New(opts)
}

// TestCreateClusterUsesEngineEndpoint proves the engine host:port is written back
// onto the cluster endpoint and every shard node, and that a delete deprovisions.
func TestCreateClusterUsesEngineEndpoint(t *testing.T) {
	eng := &recordingCacheEngine{host: "127.0.0.1", port: 6390}
	m := newEngineMock(eng)
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{
		Name: "c1", NumShards: 2, NumReplicasPerShard: 1,
	})
	requireNoError(t, err)

	if c.ClusterEndpoint.Address != "127.0.0.1" || c.ClusterEndpoint.Port != 6390 {
		t.Fatalf("cluster endpoint not overridden by engine: %+v", c.ClusterEndpoint)
	}

	for si := range c.Shards {
		for ni := range c.Shards[si].Nodes {
			ep := c.Shards[si].Nodes[ni].Endpoint
			if ep.Address != "127.0.0.1" || ep.Port != 6390 {
				t.Fatalf("shard node endpoint not overridden by engine: %+v", ep)
			}
		}
	}

	// The stored row reflects the same reachable endpoint.
	got, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)

	if len(got) != 1 || got[0].ClusterEndpoint.Address != "127.0.0.1" {
		t.Fatalf("stored cluster endpoint not backed by the engine: %+v", got)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0] != "c1" {
		t.Fatalf("unexpected provision calls: %v", eng.provisioned)
	}

	if _, err := m.DeleteCluster(ctx, "c1", ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "c1" {
		t.Fatalf("expected one deprovision for c1, got %v", eng.deprovisioned)
	}
}

// failingCacheEngine fails every Provision, to exercise the create rollback.
type failingCacheEngine struct{}

func (failingCacheEngine) Provision(_ context.Context, _ config.CacheProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{}, context.DeadlineExceeded
}

func (failingCacheEngine) Deprovision(_ context.Context, _ string) error { return nil }

// TestCreateClusterProvisionFailureRollsBack proves a failed provision leaves no
// reserved cluster behind.
func TestCreateClusterProvisionFailureRollsBack(t *testing.T) {
	m := newEngineMock(failingCacheEngine{})
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1"}); err == nil {
		t.Fatal("expected CreateCluster to fail when the engine provision fails")
	}

	got, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)

	if len(got) != 0 {
		t.Fatalf("failed provision must leave no reserved cluster, got %d", len(got))
	}
}

// blockingCacheEngine is a config.CacheEngine whose Provision blocks until
// release is signalled, announcing (once) when it has been entered. It lets a
// test hold a provision "in flight" and prove the provider lock is not held
// across it.
type blockingCacheEngine struct {
	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
}

func newBlockingCacheEngine() *blockingCacheEngine {
	return &blockingCacheEngine{entered: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingCacheEngine) Provision(_ context.Context, _ config.CacheProvisionRequest) (config.ProvisionResult, error) {
	e.enterOnce.Do(func() { close(e.entered) })
	<-e.release

	return config.ProvisionResult{Host: "127.0.0.1", Port: 6390}, nil
}

func (e *blockingCacheEngine) Deprovision(_ context.Context, _ string) error { return nil }

// TestCreateClusterReleasesLockDuringProvision proves the provider lock is
// released around the (potentially slow) engine provision: a concurrent
// DescribeClusters must return while a provision is still in flight.
func TestCreateClusterReleasesLockDuringProvision(t *testing.T) {
	eng := newBlockingCacheEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1"})
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

	got, err := m.DescribeClusters(ctx, []string{"c1"})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeClusters: %v (%d)", err, len(got))
	}

	if got[0].ClusterEndpoint.Address != "127.0.0.1" || got[0].ClusterEndpoint.Port != 6390 {
		t.Fatalf("finalized endpoint not written back: %+v", got[0].ClusterEndpoint)
	}
}
