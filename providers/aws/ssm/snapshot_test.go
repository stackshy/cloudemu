package ssm_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// TestSnapshotRoundTripSSM proves a snapshot/restore round-trip preserves a
// parameter's full version history under its original name.
func TestSnapshotRoundTripSSM(t *testing.T) {
	ctx := context.Background()
	src := ssm.New(config.NewOptions())

	if _, _, err := src.PutParameter(ctx, driver.PutConfig{Name: "/app/db", Value: "v1", Type: "String"}); err != nil {
		t.Fatalf("put v1: %v", err)
	}

	if _, _, err := src.PutParameter(ctx, driver.PutConfig{
		Name: "/app/db", Value: "v2", Type: "String", Overwrite: true,
	}); err != nil {
		t.Fatalf("put v2: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := ssm.New(config.NewOptions())
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	p, err := dst.GetParameter(ctx, "/app/db", false)
	if err != nil {
		t.Fatalf("get restored parameter: %v", err)
	}

	if p.Value != "v2" {
		t.Fatalf("restored value = %q, want v2", p.Value)
	}

	if p.Version != 2 {
		t.Fatalf("restored version = %d, want 2 (history preserved)", p.Version)
	}
}
