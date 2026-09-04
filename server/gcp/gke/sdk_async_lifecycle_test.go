package gke_test

// Real-user end-to-end coverage of the AsyncSettle cluster/node-pool lifecycle
// through google.golang.org/api/container/v1: create a cluster -> poll its
// operation RUNNING then DONE -> Get reports RUNNING with endpoint/version
// present; create a node pool -> Get reports RUNNING with initialNodeCount;
// SetSize 1->3 -> currentNodeCount reflected; List node pools; Delete a node
// pool; a node pool lookup on a missing cluster -> NOT_FOUND; Delete the
// cluster.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newAsyncSDKClient builds a GKE SDK client backed by an AsyncSettle-enabled
// mock and a FakeClock, so the test can deterministically advance past each
// settle window instead of racing a real timer.
func newAsyncSDKClient(t *testing.T) (*container.Service, string, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewGCP(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("mock-project"),
		config.WithAsyncSettle(),
	)

	srv := gcpserver.New(gcpserver.Drivers{
		GKE: cloud.GKE,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := container.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("container.NewService: %v", err)
	}

	return svc, "mock-project", fc
}

func TestSDKGKEAsyncClusterAndNodePoolLifecycle(t *testing.T) {
	svc, project, fc := newAsyncSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	clusterParent := parent(project, loc)
	clusterFull := clusterName(project, loc, "prod")

	op, err := svc.Projects.Locations.Clusters.Create(clusterParent, &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "prod", InitialNodeCount: 1},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	if op.Status != "RUNNING" {
		t.Fatalf("create op status = %q, want RUNNING (settling)", op.Status)
	}

	opFull := clusterParent + "/operations/" + op.Name

	gotOp, err := svc.Projects.Locations.Operations.Get(opFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get before settle: %v", err)
	}

	if gotOp.Status != "RUNNING" {
		t.Fatalf("op status before settle = %q, want RUNNING", gotOp.Status)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get before settle: %v", err)
	}

	if got.Status != "PROVISIONING" {
		t.Fatalf("cluster status before settle = %q, want PROVISIONING", got.Status)
	}

	// Advance the FakeClock past the cluster settle window: cluster, its
	// bootstrap node pool, and the create operation all settle together.
	fc.Advance(3 * time.Second)

	gotOp, err = svc.Projects.Locations.Operations.Get(opFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get after settle: %v", err)
	}

	if gotOp.Status != "DONE" {
		t.Fatalf("op status after settle = %q, want DONE", gotOp.Status)
	}

	got, err = svc.Projects.Locations.Clusters.Get(clusterFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get after settle: %v", err)
	}

	if got.Status != "RUNNING" {
		t.Fatalf("cluster status after settle = %q, want RUNNING", got.Status)
	}

	if got.Endpoint == "" {
		t.Fatal("expected a non-empty control-plane endpoint")
	}

	if got.CurrentMasterVersion == "" || got.CurrentNodeVersion == "" {
		t.Fatalf("expected non-empty versions, got master=%q node=%q", got.CurrentMasterVersion, got.CurrentNodeVersion)
	}

	// Create a node pool: PROVISIONING -> RUNNING over its own settle window.
	npOp, err := svc.Projects.Locations.Clusters.NodePools.Create(clusterFull, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{Name: "workers", InitialNodeCount: 1},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("NodePools.Create: %v", err)
	}

	if npOp.Status != "RUNNING" {
		t.Fatalf("node pool create op status = %q, want RUNNING (settling)", npOp.Status)
	}

	npFull := nodePoolName(project, loc, "prod", "workers")

	gotNP, err := svc.Projects.Locations.Clusters.NodePools.Get(npFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("NodePools.Get before settle: %v", err)
	}

	if gotNP.Status != "PROVISIONING" {
		t.Fatalf("node pool status before settle = %q, want PROVISIONING", gotNP.Status)
	}

	fc.Advance(2 * time.Second)

	gotNP, err = svc.Projects.Locations.Clusters.NodePools.Get(npFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("NodePools.Get after settle: %v", err)
	}

	if gotNP.Status != "RUNNING" {
		t.Fatalf("node pool status after settle = %q, want RUNNING", gotNP.Status)
	}

	if gotNP.InitialNodeCount != 1 {
		t.Fatalf("node pool initialNodeCount = %d, want 1", gotNP.InitialNodeCount)
	}

	// SetSize 1 -> 3: currentNodeCount reflects the resize immediately, even
	// while the pool briefly reports RECONCILING.
	if _, err := svc.Projects.Locations.Clusters.NodePools.SetSize(npFull, &container.SetNodePoolSizeRequest{
		NodeCount: 3,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("NodePools.SetSize: %v", err)
	}

	gotCluster, err := svc.Projects.Locations.Clusters.Get(clusterFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get after resize: %v", err)
	}

	// default-pool (1 node) + workers (3 nodes) = 4.
	if gotCluster.CurrentNodeCount != 4 {
		t.Fatalf("currentNodeCount after resize = %d, want 4", gotCluster.CurrentNodeCount)
	}

	fc.Advance(time.Second)

	gotNP, err = svc.Projects.Locations.Clusters.NodePools.Get(npFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("NodePools.Get after resize settle: %v", err)
	}

	if gotNP.Status != "RUNNING" {
		t.Fatalf("node pool status after resize settle = %q, want RUNNING", gotNP.Status)
	}

	list, err := svc.Projects.Locations.Clusters.NodePools.List(clusterFull).Context(ctx).Do()
	if err != nil {
		t.Fatalf("NodePools.List: %v", err)
	}

	if len(list.NodePools) != 2 { // default-pool + workers
		t.Fatalf("got %d node pools, want 2", len(list.NodePools))
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Delete(npFull).Context(ctx).Do(); err != nil {
		t.Fatalf("NodePools.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Get(npFull).Context(ctx).Do(); err == nil {
		t.Fatal("expected NOT_FOUND after node pool delete")
	}

	// A node pool lookup on a missing cluster must 404, not panic or leak a
	// stale entry from the deleted "prod" cluster's key space.
	missingClusterPool := nodePoolName(project, loc, "does-not-exist", "workers")

	_, err = svc.Projects.Locations.Clusters.NodePools.Get(missingClusterPool).Context(ctx).Do()
	if err == nil {
		t.Fatal("expected NOT_FOUND for a node pool under a missing cluster")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 NOT_FOUND, got %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Delete(clusterFull).Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Get(clusterFull).Context(ctx).Do(); err == nil {
		t.Fatal("expected NOT_FOUND after cluster delete")
	}
}
