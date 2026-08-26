package gcp

import (
	"context"
	"strings"
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

	npCount := int64(1)
	if _, _, err := p.GKE.CreateNodePool(ctx, "us-central1", "prod", &gke.NodePoolSpec{
		Name: "np-1", InitialNodeCount: &npCount,
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

// Two clusters with the same name in different regions must get distinct
// canonical ARNs — each embedding its own location, not the engine default.
// A substring/single-scope check can't catch a collision here.
func TestResourceDiscoveryGKESameNameDistinctARNs(t *testing.T) {
	ctx := context.Background()
	p := New()

	for _, loc := range []string{"us-central1", "europe-west1"} {
		if _, _, err := p.GKE.CreateCluster(ctx, &gke.CreateClusterInput{
			Name: "prod", Location: loc,
		}); err != nil {
			t.Fatalf("CreateCluster %s: %v", loc, err)
		}
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceKubernetes},
		Type:     resourcediscovery.TypeCluster,
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	arns := map[string]bool{}
	for _, r := range res {
		if r.ID != "prod" {
			continue
		}
		arns[r.ARN] = true
		if !strings.Contains(r.ARN, "locations/"+r.Region+"/clusters/prod") {
			t.Errorf("ARN %q does not embed its own region %q", r.ARN, r.Region)
		}
	}

	if len(arns) != 2 {
		t.Fatalf("same-name clusters in two regions collapsed to %d distinct ARNs, want 2: %v", len(arns), arns)
	}
}
