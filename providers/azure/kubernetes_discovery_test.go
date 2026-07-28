package azure

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/azure/aks"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// A real AKS managed cluster and agent pool must appear in Resource Graph —
// exercises the aksDiscovery adapter end to end.
func TestResourceDiscoverySurfacesAKS(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, err := p.AKS.CreateOrUpdateCluster(ctx, aks.ClusterInput{
		ResourceGroup: "rg-1", Name: "prod", Location: "eastus",
		Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	if _, err := p.AKS.CreateOrUpdateAgentPool(ctx, "rg-1", "prod", aks.AgentPoolInput{
		Name: "ap-1", Count: 1,
	}); err != nil {
		t.Fatalf("CreateOrUpdateAgentPool: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceKubernetes},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var cluster, agentPool bool
	for _, r := range res {
		switch {
		case r.Type == resourcediscovery.TypeCluster && r.ID == "prod":
			cluster = true
			if r.Region != "eastus" {
				t.Errorf("cluster region = %q, want eastus", r.Region)
			}
		case r.Type == resourcediscovery.TypeNodeGroup && r.ID == "ap-1":
			agentPool = true
		}
	}

	if !cluster || !agentPool {
		t.Fatalf("AKS not fully surfaced: cluster=%v agentPool=%v (%d resources)", cluster, agentPool, len(res))
	}
}
