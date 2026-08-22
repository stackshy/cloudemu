package cloudsql

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// recordingEngine is a stub config.DatabaseEngine that records calls and returns
// a fixed address, so the Cloud SQL engine wiring can be tested without a real
// database.
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

func TestCreateInstanceUsesEngineForPostgres(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pg1", Engine: "POSTGRES_15", MasterUsername: "postgres", MasterUserPassword: "secret", DBName: "app",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The engine overrides the reachable host/port carried in Endpoint...
	if inst.Endpoint != "127.0.0.1" || inst.Port != 55432 {
		t.Fatalf("endpoint not overridden by engine: got %s:%d", inst.Endpoint, inst.Port)
	}

	// ...but the "project:region:id" ConnectionName is still populated.
	if !strings.Contains(inst.ConnectionName, ":") {
		t.Fatalf("expected a project:region:id ConnectionName, got %q", inst.ConnectionName)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Username != "postgres" || eng.provisioned[0].DBName != "app" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteInstance(ctx, "pg1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "pg1" {
		t.Fatalf("expected one deprovision for pg1, got %v", eng.deprovisioned)
	}
}

func TestCreateInstanceUsesEngineForMySQL(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my1", Engine: "MYSQL_8_0"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The single wired engine now receives the MySQL family too.
	if len(eng.provisioned) != 1 || eng.provisioned[0].Engine != "MYSQL_8_0" {
		t.Fatalf("engine should back mysql, got %+v", eng.provisioned)
	}

	if inst.Endpoint != "127.0.0.1" || inst.Port != 55432 {
		t.Fatalf("mysql endpoint should be the engine host, got %s:%d", inst.Endpoint, inst.Port)
	}
}

func TestCreateInstanceSkipsEngineForUnsupportedFamily(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "ss1", Engine: "SQLSERVER_2019"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if len(eng.provisioned) != 0 {
		t.Fatalf("engine should not be used for an unsupported family, got %+v", eng.provisioned)
	}

	if inst.Endpoint == "127.0.0.1" {
		t.Fatal("unsupported-family endpoint should remain synthetic, not the engine host")
	}
}

func TestCreateInstanceNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "pg2", Engine: "POSTGRES_15"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Without an engine the reported IP stays the synthetic default and the
	// ConnectionName still carries the project:region:id identifier.
	if inst.Endpoint != syntheticPrivateIP {
		t.Fatalf("without an engine the endpoint should be the synthetic IP, got %q", inst.Endpoint)
	}

	if !strings.Contains(inst.ConnectionName, ":") {
		t.Fatalf("expected a project:region:id ConnectionName, got %q", inst.ConnectionName)
	}
}

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
