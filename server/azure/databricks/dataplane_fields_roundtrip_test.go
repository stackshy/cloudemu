package databricks_test

import (
	"context"
	"testing"

	"github.com/databricks/databricks-sdk-go/service/compute"
)

// TestSDKClusterCustomTagsAndRuntimeEngine pins issue #223: custom_tags and
// runtime_engine set on Create must survive a Get round-trip.
func TestSDKClusterCustomTagsAndRuntimeEngine(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
		ClusterName:   "c1",
		SparkVersion:  "13.3.x-scala2.12",
		NodeTypeId:    "Standard_DS3_v2",
		Autoscale:     &compute.AutoScale{MinWorkers: 2, MaxWorkers: 8},
		RuntimeEngine: compute.RuntimeEnginePhoton,
		CustomTags:    map[string]string{"team": "data"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := w.Clusters.Get(ctx, compute.GetClusterRequest{ClusterId: wait.ClusterId})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.RuntimeEngine != compute.RuntimeEnginePhoton {
		t.Fatalf("runtime_engine: got %q, want PHOTON", got.RuntimeEngine)
	}

	if got.CustomTags["team"] != "data" {
		t.Fatalf("custom_tags: got %v, want {team:data}", got.CustomTags)
	}
}

// TestSDKInstancePoolIdleAndTags pins issue #223: idle-autotermination minutes
// and custom_tags on an instance pool must survive a Get round-trip.
func TestSDKInstancePoolIdleAndTags(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.InstancePools.Create(ctx, compute.CreateInstancePool{
		InstancePoolName:                   "pool-1",
		NodeTypeId:                         "Standard_DS3_v2",
		IdleInstanceAutoterminationMinutes: 60,
		CustomTags:                         map[string]string{"team": "data"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := w.InstancePools.GetByInstancePoolId(ctx, created.InstancePoolId)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.IdleInstanceAutoterminationMinutes != 60 {
		t.Fatalf("idle_instance_autotermination_minutes: got %d, want 60",
			got.IdleInstanceAutoterminationMinutes)
	}

	if got.CustomTags["team"] != "data" {
		t.Fatalf("custom_tags: got %v, want {team:data}", got.CustomTags)
	}
}
