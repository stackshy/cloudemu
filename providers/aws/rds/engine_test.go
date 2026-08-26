package rds

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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

func TestCreateInstanceUsesEngineForPostgres(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pg1", Engine: "postgres", MasterUsername: "admin", MasterUserPassword: "secret", DBName: "app",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if inst.Endpoint != "127.0.0.1" || inst.Port != 55432 {
		t.Fatalf("endpoint not overridden by engine: got %s:%d", inst.Endpoint, inst.Port)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Username != "admin" || eng.provisioned[0].DBName != "app" {
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

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my1", Engine: "mysql"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The single wired engine now receives the MySQL family too.
	if len(eng.provisioned) != 1 || eng.provisioned[0].Engine != "mysql" {
		t.Fatalf("engine should back mysql, got %+v", eng.provisioned)
	}

	if inst.Endpoint != "127.0.0.1" || inst.Port != 55432 {
		t.Fatalf("mysql endpoint should be the engine host, got %s:%d", inst.Endpoint, inst.Port)
	}
}

func TestCreateInstanceSkipsEngineForUnsupportedFamily(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "ss1", Engine: "sqlserver-ex"})
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

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "pg2", Engine: "postgres"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if inst.Endpoint == "" || inst.Endpoint == "127.0.0.1" {
		t.Fatalf("without an engine the endpoint should be synthetic, got %q", inst.Endpoint)
	}
}

// TestAuroraClusterMemberUsesClusterCreds proves an Aurora Postgres cluster
// member is backed by the cluster's shared engine database using the CLUSTER's
// master credentials (a member carries none of its own), that the cluster's
// endpoints are pointed at the reachable engine host, and that the shared
// database is torn down once — on cluster delete, not per member.
func TestAuroraClusterMemberUsesClusterCreds(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "cl", Engine: "aurora-postgresql", MasterUsername: "root", MasterUserPassword: "clpw",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// A cluster member create carries no master creds of its own.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "m1", Engine: "aurora-postgresql", ClusterID: "cl",
	}); err != nil {
		t.Fatalf("CreateInstance member: %v", err)
	}

	// The member was provisioned with the CLUSTER's creds, keyed + named by the
	// cluster so every member shares ONE database.
	if len(eng.provisioned) != 1 {
		t.Fatalf("expected 1 provision, got %+v", eng.provisioned)
	}

	got := eng.provisioned[0]
	if got.InstanceID != "cl" || got.DBName != "cl" || got.Username != "root" || got.Password != "clpw" {
		t.Fatalf("member not provisioned with cluster creds/shared db: %+v", got)
	}

	// The cluster's endpoints now point at the reachable engine host.
	cls, err := m.DescribeClusters(ctx, []string{"cl"})
	if err != nil || len(cls) != 1 {
		t.Fatalf("DescribeClusters: %v (%d)", err, len(cls))
	}

	if cls[0].Endpoint != "127.0.0.1" || cls[0].ReaderEndpoint != "127.0.0.1" || cls[0].Port != 55432 {
		t.Fatalf("cluster endpoints not pointed at the engine: %+v", cls[0])
	}

	// Deleting the member does NOT tear down the shared database (engine keyed by
	// the cluster, not the member).
	if err := m.DeleteInstance(ctx, "m1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if len(eng.deprovisioned) != 0 {
		t.Fatalf("member delete must not deprovision the shared db, got %v", eng.deprovisioned)
	}

	// Deleting the (now empty) cluster tears the shared database down once.
	if err := m.DeleteCluster(ctx, "cl"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "cl" {
		t.Fatalf("expected one deprovision for the shared db 'cl', got %v", eng.deprovisioned)
	}
}

// TestAuroraClusterMemberMySQLUsesEngine proves an aurora-mysql cluster member
// is now engine-backed (the MySQL family joined the supported set) with the
// cluster's shared credentials, and the cluster endpoints point at the engine.
func TestAuroraClusterMemberMySQLUsesEngine(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "cl", Engine: "aurora-mysql", MasterUsername: "root", MasterUserPassword: "clpw",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "m1", Engine: "aurora-mysql", ClusterID: "cl",
	}); err != nil {
		t.Fatalf("CreateInstance member: %v", err)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].InstanceID != "cl" || eng.provisioned[0].Username != "root" {
		t.Fatalf("aurora-mysql member should be engine-backed with cluster creds, got %+v", eng.provisioned)
	}

	cls, err := m.DescribeClusters(ctx, []string{"cl"})
	if err != nil || len(cls) != 1 {
		t.Fatalf("DescribeClusters: %v (%d)", err, len(cls))
	}

	if cls[0].Endpoint != "127.0.0.1" || cls[0].Port != 55432 {
		t.Fatalf("aurora-mysql cluster endpoints should point at the engine, got %+v", cls[0])
	}
}

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

	if _, err := m.ModifyInstance(ctx, "pg", rdsdriver.ModifyInstanceInput{
		MasterUserPassword: "new", ApplyImmediately: true,
	}); err != nil {
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
