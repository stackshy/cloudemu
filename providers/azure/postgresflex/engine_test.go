package postgresflex

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

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

func TestCreateServerUsesEngine(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55555}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	// Azure PostgreSQL Flex sends no Engine field; the provider must still route
	// it to the real Postgres engine.
	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pgflex1", MasterUsername: "admin", MasterUserPassword: "secret", DBName: "app",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if inst.Endpoint != "127.0.0.1" || inst.Port != 55555 {
		t.Fatalf("endpoint not overridden by engine: got %s:%d", inst.Endpoint, inst.Port)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Username != "admin" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteInstance(ctx, "pgflex1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "pgflex1" {
		t.Fatalf("expected one deprovision for pgflex1, got %v", eng.deprovisioned)
	}
}

func TestCreateServerNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "pgflex2"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if inst.Endpoint != flexibleServerEndpoint("pgflex2") {
		t.Fatalf("without an engine the endpoint should be the synthetic FQDN, got %q", inst.Endpoint)
	}
}
