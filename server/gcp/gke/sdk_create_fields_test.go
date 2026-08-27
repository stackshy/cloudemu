// Reproduction tests for the GKE create-path field-drop bugs: cluster-level
// nodeConfig seeding the default pool (B1), node-pool management + oauthScopes
// round-tripping on create (B2), and a disabled autoscaling block surviving as
// {enabled:false} rather than a nil pointer (B3).

package gke_test

import (
	"context"
	"testing"

	"google.golang.org/api/container/v1"
)

const (
	cfMachineType = "n1-standard-4"
	cfDiskSizeGb  = 250
	cfOauthScope  = "https://www.googleapis.com/auth/cloud-platform"
)

// TestSDKGKEClusterNodeConfigSeedsDefaultPool proves the cluster-level
// nodeConfig{machineType,diskSizeGb,oauthScopes} configures the auto-created
// default node pool instead of being replaced with hardcoded defaults (B1).
func TestSDKGKEClusterNodeConfigSeedsDefaultPool(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{
			Name:             "cfg",
			InitialNodeCount: 1,
			NodeConfig: &container.NodeConfig{
				MachineType: cfMachineType,
				DiskSizeGb:  cfDiskSizeGb,
				OauthScopes: []string{cfOauthScope},
			},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "cfg")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(got.NodePools) == 0 || got.NodePools[0].Config == nil {
		t.Fatal("expected a default node pool with config")
	}

	cfg := got.NodePools[0].Config
	if cfg.MachineType != cfMachineType {
		t.Fatalf("machineType = %q, want %q", cfg.MachineType, cfMachineType)
	}

	if cfg.DiskSizeGb != cfDiskSizeGb {
		t.Fatalf("diskSizeGb = %d, want %d", cfg.DiskSizeGb, cfDiskSizeGb)
	}

	if len(cfg.OauthScopes) != 1 || cfg.OauthScopes[0] != cfOauthScope {
		t.Fatalf("oauthScopes = %v, want [%s]", cfg.OauthScopes, cfOauthScope)
	}
}

// TestSDKGKENodePoolManagementAndOAuthRoundTrip proves an explicit management
// block (autoUpgrade/autoRepair false) and config.oauthScopes survive a
// CreateNodePool round-trip (B2).
func TestSDKGKENodePoolManagementAndOAuthRoundTrip(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	parentClus := clusterName(project, loc, "mgmt")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "mgmt"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Create(parentClus, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{
			Name:             "custom",
			InitialNodeCount: 1,
			Config:           &container.NodeConfig{OauthScopes: []string{cfOauthScope}},
			Management:       &container.NodeManagement{AutoUpgrade: false, AutoRepair: false},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("nodepool create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "mgmt", "custom")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool get: %v", err)
	}

	if got.Management == nil {
		t.Fatal("expected management block")
	}

	if got.Management.AutoUpgrade || got.Management.AutoRepair {
		t.Fatalf("management = %+v, want autoUpgrade=false autoRepair=false", got.Management)
	}

	if got.Config == nil || len(got.Config.OauthScopes) != 1 || got.Config.OauthScopes[0] != cfOauthScope {
		t.Fatalf("oauthScopes not round-tripped: %+v", got.Config)
	}
}

// TestSDKGKENodePoolManagementDefaultsWhenAbsent proves a node pool created
// WITHOUT a management block still defaults autoUpgrade/autoRepair to true (B2).
func TestSDKGKENodePoolManagementDefaultsWhenAbsent(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	parentClus := clusterName(project, loc, "defs")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "defs"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Create(parentClus, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{Name: "plain", InitialNodeCount: 1},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("nodepool create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "defs", "plain")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool get: %v", err)
	}

	if got.Management == nil || !got.Management.AutoUpgrade || !got.Management.AutoRepair {
		t.Fatalf("management = %+v, want autoUpgrade=true autoRepair=true", got.Management)
	}
}

// TestSDKGKEDisabledAutoscalingEmitsBlock proves that after disabling
// autoscaling, GetNodePool returns autoscaling{enabled:false} rather than a nil
// pointer, so a client reading .Autoscaling.Enabled sees the disable (B3).
func TestSDKGKEDisabledAutoscalingEmitsBlock(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	parentClus := clusterName(project, loc, "as")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "as"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Create(parentClus, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{Name: "pool", InitialNodeCount: 1},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("nodepool create: %v", err)
	}

	npFull := nodePoolName(project, loc, "as", "pool")
	if _, err := svc.Projects.Locations.Clusters.NodePools.SetAutoscaling(npFull,
		&container.SetNodePoolAutoscalingRequest{
			Autoscaling: &container.NodePoolAutoscaling{Enabled: false},
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("setAutoscaling: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.NodePools.Get(npFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool get: %v", err)
	}

	if got.Autoscaling == nil {
		t.Fatal("autoscaling block missing after disable, want {enabled:false}")
	}

	if got.Autoscaling.Enabled {
		t.Fatalf("autoscaling.enabled = true, want false")
	}
}
