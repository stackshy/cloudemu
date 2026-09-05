package gke_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestSDKGKENodePoolNodeCountRoundTrip is the cross-service regression for GKE
// node_count drift: the Terraform google provider reads a node pool's node_count
// by summing the targetSize of the MIGs its instanceGroupUrls point at (via
// compute instanceGroupManagers). With no backing MIG the sum was always 0, so
// node_count drifted 0→N on every plan. This proves a created/resized pool emits
// instanceGroupUrls that resolve, through the compute API, to targetSize == the
// pool's node count.
func TestSDKGKENodePoolNodeCountRoundTrip(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{GKE: cloud.GKE, Compute: cloud.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	project, loc := "mock-project", "us-central1-a"

	gkeSvc, err := container.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("container.NewService: %v", err)
	}

	migClient, err := gcpcompute.NewInstanceGroupManagersRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewInstanceGroupManagersRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = migClient.Close() })

	if _, err := gkeSvc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "host"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	parentClus := clusterName(project, loc, "host")
	if _, err := gkeSvc.Projects.Locations.Clusters.NodePools.Create(parentClus, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{Name: "pool", InitialNodeCount: 2},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create node pool: %v", err)
	}

	// The node pool must advertise instanceGroupUrls; node_count resolves through
	// them, so an empty list is exactly the drift bug.
	np, err := gkeSvc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "host", "pool")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("get node pool: %v", err)
	}

	if len(np.InstanceGroupUrls) != 1 {
		t.Fatalf("instanceGroupUrls=%v want exactly 1", np.InstanceGroupUrls)
	}

	if got := resolveNodeCount(t, ctx, migClient, project, np.InstanceGroupUrls); got != 2 {
		t.Fatalf("resolved node_count=%d want 2 (drift bug: MIG targetSize not tracking pool)", got)
	}

	// Resize the pool; node_count must follow through the same MIG read.
	if _, err := gkeSvc.Projects.Locations.Clusters.NodePools.SetSize(nodePoolName(project, loc, "host", "pool"),
		&container.SetNodePoolSizeRequest{NodeCount: 4}).Context(ctx).Do(); err != nil {
		t.Fatalf("setSize: %v", err)
	}

	np, err = gkeSvc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "host", "pool")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("get node pool after resize: %v", err)
	}

	if got := resolveNodeCount(t, ctx, migClient, project, np.InstanceGroupUrls); got != 4 {
		t.Fatalf("resolved node_count after resize=%d want 4", got)
	}

	// Deleting the pool removes its backing MIG (no orphan left behind).
	if _, err := gkeSvc.Projects.Locations.Clusters.NodePools.Delete(nodePoolName(project, loc, "host", "pool")).
		Context(ctx).Do(); err != nil {
		t.Fatalf("delete node pool: %v", err)
	}

	zone, name := parseMIGURL(t, np.InstanceGroupUrls[0])
	if _, err := migClient.Get(ctx, &computepb.GetInstanceGroupManagerRequest{
		Project: project, Zone: zone, InstanceGroupManager: name,
	}); err == nil {
		t.Error("backing MIG still present after node pool delete")
	}
}

// resolveNodeCount mirrors the Terraform google provider: sum the targetSize of
// the MIGs the instanceGroupUrls point at, divided by the URL count.
func resolveNodeCount(
	t *testing.T, ctx context.Context, client *gcpcompute.InstanceGroupManagersClient,
	project string, urls []string,
) int {
	t.Helper()

	size := 0

	for _, u := range urls {
		zone, name := parseMIGURL(t, u)

		igm, err := client.Get(ctx, &computepb.GetInstanceGroupManagerRequest{
			Project: project, Zone: zone, InstanceGroupManager: name,
		})
		if err != nil {
			t.Fatalf("MIG get for %s: %v", u, err)
		}

		size += int(igm.GetTargetSize())
	}

	if len(urls) == 0 {
		return 0
	}

	return size / len(urls)
}

// parseMIGURL extracts the zone and name from a
// .../zones/{zone}/instanceGroupManagers/{name} URL.
func parseMIGURL(t *testing.T, u string) (zone, name string) {
	t.Helper()

	parts := strings.Split(u, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "zones" {
			zone = parts[i+1]
		}

		if parts[i] == "instanceGroupManagers" {
			name = parts[i+1]
		}
	}

	if zone == "" || name == "" {
		t.Fatalf("could not parse MIG URL %q", u)
	}

	return zone, name
}
