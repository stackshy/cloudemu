package aws

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// A real EKS cluster and node group, created through the mock and wired into
// the provider, must appear in the cross-service inventory — this exercises
// the eksDiscovery adapter end to end (adapter -> engine walker -> Resource).
func TestResourceDiscoverySurfacesEKS(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, err := p.EKS.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: "prod", Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := p.EKS.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "prod", NodegroupName: "ng-1",
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceKubernetes},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var cluster, nodeGroup bool
	for _, r := range res {
		switch {
		case r.Type == resourcediscovery.TypeCluster && r.ID == "prod":
			cluster = true
			if r.Tags["env"] != "prod" {
				t.Errorf("cluster tags not surfaced: %+v", r.Tags)
			}
		case r.Type == resourcediscovery.TypeNodeGroup && r.ID == "ng-1":
			nodeGroup = true
		}
	}

	if !cluster || !nodeGroup {
		t.Fatalf("EKS not fully surfaced: cluster=%v nodeGroup=%v (%d resources)", cluster, nodeGroup, len(res))
	}
}
