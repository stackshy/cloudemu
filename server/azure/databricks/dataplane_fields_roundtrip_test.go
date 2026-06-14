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

// TestSDKClusterPolicyPoolAzureSource pins issue #229: policy_id,
// instance_pool_id, azure_attributes.availability, and the backend-assigned
// cluster_source must survive a create→get round-trip.
func TestSDKClusterPolicyPoolAzureSource(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	pool, err := w.InstancePools.Create(ctx, compute.CreateInstancePool{
		InstancePoolName: "p1", NodeTypeId: "Standard_DS3_v2", MinIdleInstances: 1,
	})
	if err != nil {
		t.Fatalf("Create pool: %v", err)
	}

	wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
		ClusterName:    "c1",
		SparkVersion:   "13.3.x-scala2.12",
		NodeTypeId:     "Standard_DS3_v2",
		NumWorkers:     2,
		PolicyId:       "policy-123",
		InstancePoolId: pool.InstancePoolId,
		AzureAttributes: &compute.AzureAttributes{
			Availability: compute.AzureAvailabilityOnDemandAzure,
		},
	})
	if err != nil {
		t.Fatalf("Create cluster: %v", err)
	}

	got, err := w.Clusters.Get(ctx, compute.GetClusterRequest{ClusterId: wait.ClusterId})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.PolicyId != "policy-123" {
		t.Fatalf("policy_id: got %q, want policy-123", got.PolicyId)
	}

	if got.InstancePoolId != pool.InstancePoolId {
		t.Fatalf("instance_pool_id: got %q, want %q", got.InstancePoolId, pool.InstancePoolId)
	}

	if got.AzureAttributes == nil || got.AzureAttributes.Availability != compute.AzureAvailabilityOnDemandAzure {
		t.Fatalf("azure_attributes.availability: got %+v, want ON_DEMAND_AZURE", got.AzureAttributes)
	}

	if got.ClusterSource != compute.ClusterSourceApi {
		t.Fatalf("cluster_source: got %q, want API", got.ClusterSource)
	}

	// The issue reports the drop on create->list, so pin the list path too
	// (it shares the same response converter as Get).
	all, err := w.Clusters.ListAll(ctx, compute.ListClustersRequest{})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(all) != 1 {
		t.Fatalf("got %d clusters, want 1", len(all))
	}

	listed := all[0]
	if listed.PolicyId != "policy-123" || listed.InstancePoolId != pool.InstancePoolId ||
		listed.ClusterSource != compute.ClusterSourceApi ||
		listed.AzureAttributes == nil ||
		listed.AzureAttributes.Availability != compute.AzureAvailabilityOnDemandAzure {
		t.Fatalf("list dropped a field: %+v (azure=%+v)", listed, listed.AzureAttributes)
	}
}
