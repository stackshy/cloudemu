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

func TestCreateInstanceSkipsEngineForNonPostgres(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my1", Engine: "mysql"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if len(eng.provisioned) != 0 {
		t.Fatalf("engine should not be used for mysql, got %+v", eng.provisioned)
	}

	if inst.Endpoint == "127.0.0.1" {
		t.Fatal("mysql endpoint should remain synthetic, not the engine host")
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
