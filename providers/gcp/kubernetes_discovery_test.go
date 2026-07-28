package gcp

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// A real GKE cluster and node pool must appear in Cloud Asset inventory —
// exercises the gkeDiscovery adapter end to end.
func TestResourceDiscoverySurfacesGKE(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, _, err := p.GKE.CreateCluster(ctx, &gke.CreateClusterInput{
		Name: "prod", Location: "us-central1",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, _, err := p.GKE.CreateNodePool(ctx, "us-central1", "prod", &gke.NodePoolSpec{
		Name: "np-1", InitialNodeCount: 1,
	}); err != nil {
		t.Fatalf("CreateNodePool: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceKubernetes},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var cluster, nodePool bool
	for _, r := range res {
		switch {
		case r.Type == resourcediscovery.TypeCluster && r.ID == "prod":
			cluster = true
			if r.Region != "us-central1" {
				t.Errorf("cluster region = %q, want us-central1", r.Region)
			}
		case r.Type == resourcediscovery.TypeNodeGroup && r.ID == "np-1":
			nodePool = true
		}
	}

	if !cluster || !nodePool {
		t.Fatalf("GKE not fully surfaced: cluster=%v nodePool=%v (%d resources)", cluster, nodePool, len(res))
	}
}
