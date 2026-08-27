// Real-container-SDK parity tests for four GKE fidelity fixes: an explicit
// initialNodeCount=0 surviving create (autoscale-from-zero), cluster
// labelFingerprint round-trip + optimistic-concurrency enforcement on
// setResourceLabels, and a cluster desiredNodeVersion propagating to node pools.

package gke_test

import (
	"context"
	"testing"

	"google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
)

// TestSDKGKENodePoolInitialNodeCountZero proves an explicitly-requested
// initialNodeCount=0 (an autoscale-from-zero pool) survives create instead of
// being clobbered to 1. The SDK omits a zero int unless it is force-sent, so
// ForceSendFields reproduces what Terraform's provider does.
func TestSDKGKENodePoolInitialNodeCountZero(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	parentClus := clusterName(project, loc, "zero")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "zero"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Create(parentClus, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{
			Name:             "scaler",
			InitialNodeCount: 0,
			ForceSendFields:  []string{"InitialNodeCount"},
			Autoscaling: &container.NodePoolAutoscaling{
				Enabled:         true,
				MinNodeCount:    0,
				MaxNodeCount:    5,
				ForceSendFields: []string{"MinNodeCount"},
			},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("nodepool create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "zero", "scaler")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool get: %v", err)
	}

	if got.InitialNodeCount != 0 {
		t.Fatalf("initialNodeCount = %d, want 0", got.InitialNodeCount)
	}
}

// TestSDKGKEDefaultPoolInitialNodeCountZero proves a cluster created with an
// explicit initialNodeCount=0 bootstraps its default pool at 0 nodes rather
// than defaulting to 1.
func TestSDKGKEDefaultPoolInitialNodeCountZero(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{
			Name:             "zdef",
			InitialNodeCount: 0,
			ForceSendFields:  []string{"InitialNodeCount"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "zdef")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(got.NodePools) != 1 {
		t.Fatalf("got %d pools, want 1 default pool", len(got.NodePools))
	}

	if got.NodePools[0].InitialNodeCount != 0 {
		t.Fatalf("default pool initialNodeCount = %d, want 0", got.NodePools[0].InitialNodeCount)
	}
}

// TestSDKGKELabelFingerprintRoundTripAndEnforcement proves a cluster GET
// returns a labelFingerprint, that setResourceLabels rejects a stale
// fingerprint, and that the current fingerprint is accepted.
func TestSDKGKELabelFingerprintRoundTripAndEnforcement(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	name := clusterName(project, loc, "labels")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "labels", ResourceLabels: map[string]string{"env": "prod"}},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.LabelFingerprint == "" {
		t.Fatal("expected a non-empty labelFingerprint on cluster GET")
	}

	// A stale fingerprint must be rejected with FAILED_PRECONDITION (HTTP 409).
	_, err = svc.Projects.Locations.Clusters.SetResourceLabels(name, &container.SetLabelsRequest{
		ResourceLabels:   map[string]string{"env": "staging"},
		LabelFingerprint: "deadbeefdeadbeef",
	}).Context(ctx).Do()
	if err == nil {
		t.Fatal("expected setResourceLabels with stale fingerprint to fail")
	}

	if gerr, ok := err.(*googleapi.Error); ok && gerr.Code != 409 {
		t.Fatalf("stale fingerprint error code = %d, want 409", gerr.Code)
	}

	// The current fingerprint is accepted.
	if _, err := svc.Projects.Locations.Clusters.SetResourceLabels(name, &container.SetLabelsRequest{
		ResourceLabels:   map[string]string{"env": "staging"},
		LabelFingerprint: got.LabelFingerprint,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("setResourceLabels with current fingerprint: %v", err)
	}

	after, err := svc.Projects.Locations.Clusters.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get after label change: %v", err)
	}

	if after.ResourceLabels["env"] != "staging" {
		t.Fatalf("labels not applied: %v", after.ResourceLabels)
	}

	if after.LabelFingerprint == got.LabelFingerprint {
		t.Fatal("labelFingerprint should change when labels change")
	}
}

// TestSDKGKEClusterNodeVersionPropagatesToPools proves a cluster-level
// desiredNodeVersion update rolls the version of the targeted node pool.
func TestSDKGKEClusterNodeVersionPropagatesToPools(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	name := clusterName(project, loc, "ver")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "ver"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	const newVersion = "1.29.0-gke.0"

	if _, err := svc.Projects.Locations.Clusters.Update(name, &container.UpdateClusterRequest{
		Update: &container.ClusterUpdate{
			DesiredNodeVersion: newVersion,
			DesiredNodePoolId:  "default-pool",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("update cluster: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "ver", "default-pool")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool get: %v", err)
	}

	if got.Version != newVersion {
		t.Fatalf("node pool version = %q, want %q", got.Version, newVersion)
	}
}
