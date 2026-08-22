package rds

import (
	"context"
	"testing"

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
