package rds

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// blockingEngine is a config.DatabaseEngine whose Provision blocks until release
// is signalled, announcing (once) when it has been entered. It lets a test hold
// a provision "in flight" and prove the provider lock is not held across it.
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

// TestCreateInstanceReleasesLockDuringProvision proves the provider lock is
// released around the (potentially slow) engine provision: a concurrent
// DescribeInstances must return while a provision is still in flight.
func TestCreateInstanceReleasesLockDuringProvision(t *testing.T) {
	eng := newBlockingEngine()
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "pg1", Engine: "postgres"})
		created <- err
	}()

	select {
	case <-eng.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provision never started")
	}

	describeReturned := make(chan struct{})

	go func() {
		_, _ = m.DescribeInstances(ctx, nil)
		close(describeReturned)
	}()

	select {
	case <-describeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("DescribeInstances blocked while a provision held the provider lock")
	}

	close(eng.release)

	if err := <-created; err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	got, err := m.DescribeInstances(ctx, []string{"pg1"})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeInstances: %v (%d)", err, len(got))
	}

	if got[0].Endpoint != "127.0.0.1" || got[0].Port != 55432 {
		t.Fatalf("finalized endpoint not written back: %s:%d", got[0].Endpoint, got[0].Port)
	}
}

// TestCreateInstanceProvisionFailureRollsBack proves a failed provision leaves
// no reserved row behind.
func TestCreateInstanceProvisionFailureRollsBack(t *testing.T) {
	eng := &failingEngine{}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "pg1", Engine: "postgres"}); err == nil {
		t.Fatal("expected CreateInstance to fail when the engine provision fails")
	}

	got, err := m.DescribeInstances(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("failed provision must leave no reserved row, got %d", len(got))
	}
}

type failingEngine struct{}

func (failingEngine) Provision(_ context.Context, _ config.ProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{}, context.DeadlineExceeded
}

func (failingEngine) Deprovision(_ context.Context, _ string) error { return nil }

// TestRestoreInstanceFromSnapshotProvisions proves a snapshot restore backs the
// new instance with a real database using the source's inherited credentials,
// so the reported endpoint is reachable rather than resolving to nothing.
func TestRestoreInstanceFromSnapshotProvisions(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "src", Engine: "postgres", MasterUsername: "admin", MasterUserPassword: "pw",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap", InstanceID: "src"}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	inst, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored", SnapshotID: "snap",
	})
	if err != nil {
		t.Fatalf("RestoreInstanceFromSnapshot: %v", err)
	}

	if inst.Endpoint != "127.0.0.1" || inst.Port != 55432 {
		t.Fatalf("restored endpoint not backed by the engine: %s:%d", inst.Endpoint, inst.Port)
	}

	last := lastProvision(t, eng, "restored")
	if last.Username != "admin" || last.Password != "pw" {
		t.Fatalf("restore did not inherit source credentials: %+v", last)
	}
}

// TestModifyInstanceRotatesPassword proves ModifyInstance re-runs the engine
// upsert with the new master password (and skips the engine when none is given).
func TestModifyInstanceRotatesPassword(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pg", Engine: "postgres", MasterUsername: "admin", MasterUserPassword: "old",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.ModifyInstance(ctx, "pg", rdsdriver.ModifyInstanceInput{MasterUserPassword: "new"}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	last := lastProvision(t, eng, "pg")
	if last.Username != "admin" || last.Password != "new" {
		t.Fatalf("password rotation not sent to the engine: %+v", last)
	}

	before := len(eng.provisioned)

	if _, err := m.ModifyInstance(ctx, "pg", rdsdriver.ModifyInstanceInput{InstanceClass: "db.t3.large"}); err != nil {
		t.Fatalf("ModifyInstance (no password): %v", err)
	}

	if len(eng.provisioned) != before {
		t.Fatalf("a modify without a password must not touch the engine, got %d extra", len(eng.provisioned)-before)
	}
}

func TestModifyClusterRotatesPassword(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "aur", Engine: "aurora-postgresql", MasterUsername: "admin", MasterUserPassword: "old",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.ModifyCluster(ctx, "aur", rdsdriver.ModifyInstanceInput{MasterUserPassword: "new"}); err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	last := lastProvision(t, eng, "aur")
	if last.Username != "admin" || last.Password != "new" {
		t.Fatalf("cluster password rotation not sent to the engine: %+v", last)
	}

	before := len(eng.provisioned)

	if _, err := m.ModifyCluster(ctx, "aur", rdsdriver.ModifyInstanceInput{EngineVersion: "15.4"}); err != nil {
		t.Fatalf("ModifyCluster (no password): %v", err)
	}

	if len(eng.provisioned) != before {
		t.Fatalf("a cluster modify without a password must not touch the engine, got %d extra", len(eng.provisioned)-before)
	}
}

// lastProvision returns the most recent provision request recorded for id.
func lastProvision(t *testing.T, eng *recordingEngine, id string) config.ProvisionRequest {
	t.Helper()

	for i := len(eng.provisioned) - 1; i >= 0; i-- {
		if eng.provisioned[i].InstanceID == id {
			return eng.provisioned[i]
		}
	}

	t.Fatalf("no provision recorded for %q: %+v", id, eng.provisioned)

	return config.ProvisionRequest{}
}
