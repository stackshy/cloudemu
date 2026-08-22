package alloydb

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// recordingEngine is a stub config.DatabaseEngine that records calls and returns
// a fixed address, so the AlloyDB engine wiring can be tested without a real
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

// TestAlloyDBInstanceUsesEngine proves an AlloyDB instance created via the
// AlloyDB-native path (the one the Admin SDK drives) is backed by the real
// engine using the cluster's initial user/password, that its reported IPAddress
// becomes the engine host, and that delete tears the database down.
func TestAlloyDBInstanceUsesEngine(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 5432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateAlloyDBCluster(ctx, rdsdriver.AlloyDBClusterConfig{
		ID: "c1", DatabaseVersion: "POSTGRES_15", InitialUser: "postgres", InitialPassword: "secret",
	}); err != nil {
		t.Fatalf("CreateAlloyDBCluster: %v", err)
	}

	if _, err := m.CreateAlloyDBInstance(ctx, rdsdriver.AlloyDBInstanceConfig{
		ClusterID: "c1", ID: "i1", InstanceType: "PRIMARY",
	}); err != nil {
		t.Fatalf("CreateAlloyDBInstance: %v", err)
	}

	// The engine backed the instance with the cluster's initial creds, keyed by
	// "{cluster}/{instance}".
	if len(eng.provisioned) != 1 {
		t.Fatalf("expected 1 provision, got %+v", eng.provisioned)
	}

	got := eng.provisioned[0]
	if got.InstanceID != "c1/i1" || got.Username != "postgres" || got.Password != "secret" {
		t.Fatalf("instance not provisioned with cluster creds: %+v", got)
	}

	// The SDK-surfaced IPAddress is the reachable engine host.
	info, err := m.AlloyDBInstanceInfo(ctx, "c1", "i1")
	if err != nil {
		t.Fatalf("AlloyDBInstanceInfo: %v", err)
	}

	if info.IPAddress != "127.0.0.1" {
		t.Fatalf("IPAddress not overridden by engine: got %q", info.IPAddress)
	}

	if err := m.DeleteInstance(ctx, "c1/i1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "c1/i1" {
		t.Fatalf("expected one deprovision for c1/i1, got %v", eng.deprovisioned)
	}
}

// TestAlloyDBInstancePortablePathUsesEngine proves the portable CreateInstance
// path is engine-backed the same way as the native path.
func TestAlloyDBInstancePortablePathUsesEngine(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 5432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "c1", EngineVersion: "POSTGRES_15", MasterUsername: "postgres", MasterUserPassword: "secret",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ClusterID: "c1", ID: "i1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Username != "postgres" || eng.provisioned[0].Password != "secret" {
		t.Fatalf("instance not provisioned with cluster creds: %+v", eng.provisioned)
	}

	info, err := m.AlloyDBInstanceInfo(ctx, "c1", "i1")
	if err != nil {
		t.Fatalf("AlloyDBInstanceInfo: %v", err)
	}

	if info.IPAddress != "127.0.0.1" {
		t.Fatalf("IPAddress not overridden by engine: got %q", info.IPAddress)
	}

	// Cluster delete cascades and tears the instance's database down.
	if err := m.DeleteCluster(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "c1/i1" {
		t.Fatalf("expected cascade deprovision for c1/i1, got %v", eng.deprovisioned)
	}
}

// TestAlloyDBInstanceNoEngineIsSynthetic proves that without an engine the
// reported IPAddress stays the synthetic default.
func TestAlloyDBInstanceNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	if _, err := m.CreateAlloyDBCluster(ctx, rdsdriver.AlloyDBClusterConfig{
		ID: "c1", DatabaseVersion: "POSTGRES_15",
	}); err != nil {
		t.Fatalf("CreateAlloyDBCluster: %v", err)
	}

	if _, err := m.CreateAlloyDBInstance(ctx, rdsdriver.AlloyDBInstanceConfig{
		ClusterID: "c1", ID: "i1", InstanceType: "PRIMARY",
	}); err != nil {
		t.Fatalf("CreateAlloyDBInstance: %v", err)
	}

	info, err := m.AlloyDBInstanceInfo(ctx, "c1", "i1")
	if err != nil {
		t.Fatalf("AlloyDBInstanceInfo: %v", err)
	}

	if info.IPAddress != syntheticInstanceIP {
		t.Fatalf("without an engine the IPAddress should be synthetic, got %q", info.IPAddress)
	}
}
