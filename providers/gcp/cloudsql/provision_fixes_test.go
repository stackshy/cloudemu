package cloudsql

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

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
// released around the engine provision so concurrent reads never block.
func TestCreateInstanceReleasesLockDuringProvision(t *testing.T) {
	eng := newBlockingEngine()
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "pg1", Engine: "POSTGRES_15"})
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

// TestCloneInstanceProvisionsDistinctDatabase proves a clone is backed by its
// OWN engine database (named after the clone), never the source's — the alias
// that let clone writes corrupt the source and a clone delete DROP the source.
func TestCloneInstanceProvisionsDistinctDatabase(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "src", Engine: "POSTGRES_15", MasterUsername: "postgres", MasterUserPassword: "pw", DBName: "appdb",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CloneInstance(ctx, "src", "dest"); err != nil {
		t.Fatalf("CloneInstance: %v", err)
	}

	srcReq := lastProvision(t, eng, "src")
	cloneReq := lastProvision(t, eng, "dest")

	if cloneReq.DBName == srcReq.DBName {
		t.Fatalf("clone aliases the source database %q", srcReq.DBName)
	}

	if cloneReq.DBName != "dest" {
		t.Fatalf("clone should provision its own database named after the clone, got %q", cloneReq.DBName)
	}
}

// TestRestoreInstanceFromSnapshotProvisions proves a restore backs the new
// instance with a real database (inheriting the source's login), so its reported
// IP is reachable rather than synthetic.
func TestRestoreInstanceFromSnapshotProvisions(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "src", Engine: "POSTGRES_15", MasterUsername: "postgres", MasterUserPassword: "pw",
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
		t.Fatalf("restored IP not backed by the engine: %s:%d", inst.Endpoint, inst.Port)
	}

	last := lastProvision(t, eng, "restored")
	if last.Username != "postgres" || last.Password != "pw" {
		t.Fatalf("restore did not inherit source credentials: %+v", last)
	}
}

// TestModifyInstanceRotatesPassword proves ModifyInstance re-runs the engine
// upsert with the new master password.
func TestModifyInstanceRotatesPassword(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pg", Engine: "POSTGRES_15", MasterUsername: "postgres", MasterUserPassword: "old",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.ModifyInstance(ctx, "pg", rdsdriver.ModifyInstanceInput{MasterUserPassword: "new"}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	last := lastProvision(t, eng, "pg")
	if last.Password != "new" || last.Username != "postgres" {
		t.Fatalf("password rotation not sent to the engine: %+v", last)
	}
}

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
