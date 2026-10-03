package loadbalancer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

func TestPatchersUseRequestProject(t *testing.T) {
	m := newTestMock()
	ctxA := projectctx.WithProject(context.Background(), "p-a")
	ctxB := projectctx.WithProject(context.Background(), "p-b")

	for _, ctx := range []context.Context{ctxA, ctxB} {
		if _, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "fr", Type: "network"}); err != nil {
			t.Fatalf("CreateLoadBalancer: %v", err)
		}

		if _, err := m.CreateTargetGroup(ctx, driver.TargetGroupConfig{Name: "bs", Protocol: "TCP"}); err != nil {
			t.Fatalf("CreateTargetGroup: %v", err)
		}
	}

	if err := m.PatchGCPForwardingRule(ctxB, "fr", func(lb *driver.LBInfo) error {
		lb.Tags["k"] = "b"
		return nil
	}); err != nil {
		t.Fatalf("PatchGCPForwardingRule: %v", err)
	}

	if err := m.PatchGCPBackendService(ctxB, "bs", func(tg *driver.TargetGroupInfo) { tg.Tags["k"] = "b" }); err != nil {
		t.Fatalf("PatchGCPBackendService: %v", err)
	}

	tests := []struct {
		ctx     context.Context
		project string
		want    string
	}{
		{ctxA, "p-a", ""},
		{ctxB, "p-b", "b"},
	}

	for _, tc := range tests {
		lbs, _ := m.DescribeLoadBalancers(tc.ctx, nil)
		tgs, _ := m.DescribeTargetGroups(tc.ctx, nil)

		if len(lbs) != 1 || len(tgs) != 1 {
			t.Fatalf("%s: %d forwarding rules, %d backend services, want 1 each", tc.project, len(lbs), len(tgs))
		}

		if lbs[0].Tags["k"] != tc.want || tgs[0].Tags["k"] != tc.want {
			t.Errorf("%s: fr tag %q, bs tag %q, want %q", tc.project, lbs[0].Tags["k"], tgs[0].Tags["k"], tc.want)
		}
	}

	all, _ := m.DescribeLoadBalancers(projectctx.AllProjects(context.Background()), nil)
	if len(all) != 2 {
		t.Errorf("AllProjects: %d forwarding rules, want 2", len(all))
	}
}

func TestOpaqueResourcesPerProject(t *testing.T) {
	m := newTestMock()
	ctxA := projectctx.WithProject(context.Background(), "p-a")
	ctxB := projectctx.WithProject(context.Background(), "p-b")
	res := driver.GCPResource{Collection: "healthChecks", Scope: "global", Name: "hc"}

	for _, ctx := range []context.Context{ctxA, ctxB} {
		if err := m.PutGCPResource(ctx, res); err != nil {
			t.Fatalf("PutGCPResource: %v", err)
		}
	}

	if err := m.DeleteGCPResource(ctxB, "healthChecks", "global", "hc"); err != nil {
		t.Fatalf("DeleteGCPResource: %v", err)
	}

	if _, err := m.GetGCPResource(ctxA, "healthChecks", "global", "hc"); err != nil {
		t.Errorf("p-a hc after p-b delete: %v", err)
	}

	if items, _ := m.ListGCPResources(ctxB, "healthChecks", "global"); len(items) != 0 {
		t.Errorf("p-b list = %d items, want 0", len(items))
	}
}

// TestRestoreAdoptsLegacyResources loads a snapshot whose opaque resources were
// keyed before project scoping and checks they land in the default project.
func TestRestoreAdoptsLegacyResources(t *testing.T) {
	legacy := map[string]driver.GCPResource{
		"healthChecks\x00global\x00hc": {Collection: "healthChecks", Scope: "global", Name: "hc", ID: "1"},
	}

	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	snap, err := json.Marshal(lbSnapshot{GCPResources: raw})
	if err != nil {
		t.Fatal(err)
	}

	m := newTestMock()
	if err := m.Restore(context.Background(), snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := m.GetGCPResource(context.Background(), "healthChecks", "global", "hc")
	if err != nil || got.ID != "1" {
		t.Fatalf("legacy hc in default project = %+v, %v", got, err)
	}

	if _, err := m.GetGCPResource(projectctx.WithProject(context.Background(), "other"), "healthChecks", "global", "hc"); err == nil {
		t.Error("legacy hc visible in another project")
	}
}
