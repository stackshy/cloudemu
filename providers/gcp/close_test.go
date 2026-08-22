package gcp_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
)

// closeSpyEngine is a config.DatabaseEngine that also implements io.Closer and
// records whether Close was called, so Provider.Close cascading can be asserted
// without a real backing engine.
type closeSpyEngine struct{ closed int }

func (e *closeSpyEngine) Provision(context.Context, config.ProvisionRequest) (config.ProvisionResult, error) {
	return config.ProvisionResult{}, nil
}

func (e *closeSpyEngine) Deprovision(context.Context, string) error { return nil }

func (e *closeSpyEngine) Close() error {
	e.closed++

	return nil
}

func TestProviderCloseCascadesToEngine(t *testing.T) {
	eng := &closeSpyEngine{}

	p := gcp.New(config.WithDatabaseEngine(eng))
	if err := p.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if eng.closed != 1 {
		t.Fatalf("engine Close called %d times, want 1", eng.closed)
	}
}

func TestProviderCloseNoEngineIsNoOp(t *testing.T) {
	if err := gcp.New().Close(); err != nil {
		t.Fatalf("Close with no engine wired returned error: %v", err)
	}
}
