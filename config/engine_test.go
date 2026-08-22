package config_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
)

type stubEngine struct{}

func (stubEngine) Provision(_ context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{Host: "localhost", Port: 5432}, nil
}

func (stubEngine) Deprovision(_ context.Context, _ string) error { return nil }

func TestDatabaseEngineDefaultsNil(t *testing.T) {
	if config.NewOptions().DatabaseEngine != nil {
		t.Fatal("DatabaseEngine should default to nil (in-memory)")
	}
}

func TestWithDatabaseEngine(t *testing.T) {
	o := config.NewOptions(config.WithDatabaseEngine(stubEngine{}))
	if o.DatabaseEngine == nil {
		t.Fatal("WithDatabaseEngine did not set the engine")
	}

	res, err := o.DatabaseEngine.Provision(context.Background(), config.ProvisionRequest{InstanceID: "db1", Engine: "postgres"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if res.Host != "localhost" || res.Port != 5432 {
		t.Fatalf("unexpected provision result: %+v", res)
	}
}
