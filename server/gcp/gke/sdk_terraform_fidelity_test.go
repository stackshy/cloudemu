// Real-user e2e regression tests: the official Terraform google provider
// (google_container_cluster / google_container_node_pool) dereferences
// cluster.LegacyAbac.Enabled and cluster.NetworkConfig.Network/Subnetwork
// unconditionally on read, so a cluster response missing either object panics
// the provider on the first apply. These tests lock in that both objects are
// always emitted, plus deterministic list ordering and a request-scoped
// operation targetLink project.

package gke_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/api/container/v1"
)

// TestSDKGKEClusterAlwaysEmitsLegacyAbacAndNetworkConfig proves a cluster read
// always carries a non-nil legacyAbac and networkConfig (with network/
// subnetwork), the fields the Terraform provider reads without a nil guard.
func TestSDKGKEClusterAlwaysEmitsLegacyAbacAndNetworkConfig(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "tf", InitialNodeCount: 1},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "tf")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.LegacyAbac == nil {
		t.Fatal("legacyAbac nil — Terraform provider would panic on cluster.LegacyAbac.Enabled")
	}

	if got.NetworkConfig == nil {
		t.Fatal("networkConfig nil — Terraform provider would panic on cluster.NetworkConfig.Network")
	}

	if got.NetworkConfig.Network == "" || got.NetworkConfig.Subnetwork == "" {
		t.Fatalf("networkConfig network/subnetwork empty: %q / %q",
			got.NetworkConfig.Network, got.NetworkConfig.Subnetwork)
	}

	// The same objects must survive the list path (Terraform refreshes via both).
	list, err := svc.Projects.Locations.Clusters.List(parent(project, loc)).Context(ctx).Do()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list.Clusters) != 1 || list.Clusters[0].LegacyAbac == nil || list.Clusters[0].NetworkConfig == nil {
		t.Fatalf("list cluster missing legacyAbac/networkConfig: %+v", list.Clusters)
	}
}

// TestSDKGKEOperationTargetLinkUsesRequestProject proves an operation's
// targetLink carries the project from the request URL, not the emulator's
// configured default project — a user parsing targetLink to locate the resource
// must see their own project.
func TestSDKGKEOperationTargetLinkUsesRequestProject(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	op, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "proj"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	want := "projects/" + project + "/locations/" + loc + "/clusters/proj"
	if !strings.Contains(op.TargetLink, want) {
		t.Fatalf("targetLink = %q, want it to contain %q", op.TargetLink, want)
	}
}

// TestSDKGKENodePoolListDeterministicOrder proves nodePools.list returns pools
// in a stable creation order (default-pool first, then added pools) rather than
// random map-iteration order, so repeated reads and snapshots don't drift.
func TestSDKGKENodePoolListDeterministicOrder(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "ord", InitialNodeCount: 1},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, name := range []string{"pool-b", "pool-a"} {
		if _, err := svc.Projects.Locations.Clusters.NodePools.Create(
			clusterName(project, loc, "ord"),
			&container.CreateNodePoolRequest{NodePool: &container.NodePool{Name: name, InitialNodeCount: 1}},
		).Context(ctx).Do(); err != nil {
			t.Fatalf("create node pool %s: %v", name, err)
		}
	}

	var prev []string

	for i := 0; i < 5; i++ {
		resp, err := svc.Projects.Locations.Clusters.NodePools.List(clusterName(project, loc, "ord")).Context(ctx).Do()
		if err != nil {
			t.Fatalf("list node pools: %v", err)
		}

		names := make([]string, 0, len(resp.NodePools))
		for _, np := range resp.NodePools {
			names = append(names, np.Name)
		}

		if prev != nil && strings.Join(names, ",") != strings.Join(prev, ",") {
			t.Fatalf("node pool order not stable: %v vs %v", prev, names)
		}

		prev = names
	}

	// default-pool was created first, so it must sort ahead of the later pools.
	if len(prev) == 0 || prev[0] != "default-pool" {
		t.Fatalf("expected default-pool first, got %v", prev)
	}
}

// TestSDKGKEClusterListDeterministicOrder proves clusters.list returns clusters
// sorted by name across repeated calls.
func TestSDKGKEClusterListDeterministicOrder(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	for _, name := range []string{"gamma", "alpha", "beta"} {
		if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
			Cluster: &container.Cluster{Name: name},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	resp, err := svc.Projects.Locations.Clusters.List(parent(project, loc)).Context(ctx).Do()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	got := make([]string, 0, len(resp.Clusters))
	for _, c := range resp.Clusters {
		got = append(got, c.Name)
	}

	want := "alpha,beta,gamma"
	if strings.Join(got, ",") != want {
		t.Fatalf("cluster order = %v, want %s", got, want)
	}
}
