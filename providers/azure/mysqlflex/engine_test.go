package mysqlflex

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// recordingEngine is a stub config.DatabaseEngine that records calls and returns
// a fixed address, so the MySQL Flexible Server engine wiring can be tested
// without a real database (no Docker in core CI).
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

func TestCreateInstanceUsesEngineForMySQL(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 33060}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "app-my", Engine: "MySQL", MasterUsername: "myadmin", MasterUserPassword: "secret", DBName: "app",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The engine overrides the reachable host/port carried in Endpoint (which the
	// wire handler surfaces as fullyQualifiedDomainName), replacing the synthetic
	// <name>.mysql.database.azure.com FQDN.
	if inst.Endpoint != "127.0.0.1" || inst.Port != 33060 {
		t.Fatalf("endpoint not overridden by engine: got %s:%d", inst.Endpoint, inst.Port)
	}

	if len(eng.provisioned) != 1 ||
		eng.provisioned[0].Username != "myadmin" ||
		eng.provisioned[0].DBName != "app" ||
		eng.provisioned[0].Engine != "MySQL" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteInstance(ctx, "app-my"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "app-my" {
		t.Fatalf("expected one deprovision for app-my, got %v", eng.deprovisioned)
	}
}

func TestCreateInstanceDefaultsEngineToMySQL(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 33060}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))

	// An empty Engine defaults to "MySQL", so the engine is still invoked.
	if _, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Engine != defaultEngine {
		t.Fatalf("engine should back the defaulted MySQL family, got %+v", eng.provisioned)
	}
}

func TestCreateInstanceNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my2", Engine: "MySQL"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Without an engine the reported FQDN stays the synthetic Azure endpoint.
	if inst.Endpoint != "my2"+endpointSuffix {
		t.Fatalf("without an engine the endpoint should be the synthetic FQDN, got %q", inst.Endpoint)
	}
}
