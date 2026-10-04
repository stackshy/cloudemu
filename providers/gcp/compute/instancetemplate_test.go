package compute

import (
	"context"
	"encoding/json"
	"testing"
)

// TestInstanceTemplatesAndRegionalMIGSurviveSnapshot proves instance templates
// and regional MIGs (with their stored spec) round-trip a snapshot.
func TestInstanceTemplatesAndRegionalMIGSurviveSnapshot(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	spec := json.RawMessage(`{"name":"it1","properties":{"machineType":"e2-small"}}`)
	if err := m.CreateInstanceTemplateGCP(InstanceTemplate{Name: "it1", Spec: spec}); err != nil {
		t.Fatalf("CreateInstanceTemplateGCP: %v", err)
	}

	if err := m.CreateInstanceGroupManagerGCP(InstanceGroupManager{
		Name: "rmig", Region: "us-central1", TargetSize: 2, InstanceTemplate: "it1", Spec: json.RawMessage(`{"name":"rmig"}`),
	}); err != nil {
		t.Fatalf("CreateInstanceGroupManagerGCP: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newTestMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, ok := restored.GetInstanceTemplateGCP("", "it1")
	if !ok || string(got.Spec) != string(spec) {
		t.Fatalf("template after restore: ok=%v spec=%s", ok, got.Spec)
	}

	igm, ok := restored.GetInstanceGroupManagerGCP("", "us-central1", "rmig")
	if !ok || igm.Region != "us-central1" || igm.Zone != "" || igm.TargetSize != 2 {
		t.Fatalf("regional MIG after restore: ok=%v %+v", ok, igm)
	}

	if err := restored.DeleteInstanceTemplateGCP("", "it1"); err == nil {
		t.Fatal("delete template in use: want error")
	}
}
