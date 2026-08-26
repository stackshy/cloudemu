package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

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
		Name: "ap-1", Count: to.Ptr[int32](1),
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
			// The ARM ID must carry the cluster's real resource group, not
			// the discovery default.
			if !strings.Contains(r.ARN, "resourceGroups/rg-1/") {
				t.Errorf("cluster ARN %q missing real resource group rg-1", r.ARN)
			}
		case r.Type == resourcediscovery.TypeNodeGroup && r.ID == "ap-1":
			agentPool = true
		}
	}

	if !cluster || !agentPool {
		t.Fatalf("AKS not fully surfaced: cluster=%v agentPool=%v (%d resources)", cluster, agentPool, len(res))
	}
}

// Two clusters with the same name in different resource groups must get
// distinct canonical ARNs — each embedding its own resource group, not the
// discovery default. The AKS mock keys on rg+name, so this is a real case.
func TestResourceDiscoveryAKSSameNameDistinctARNs(t *testing.T) {
	ctx := context.Background()
	p := New()

	for _, rg := range []string{"rg-a", "rg-b"} {
		if _, err := p.AKS.CreateOrUpdateCluster(ctx, aks.ClusterInput{
			ResourceGroup: rg, Name: "prod", Location: "eastus",
		}); err != nil {
			t.Fatalf("CreateOrUpdateCluster %s: %v", rg, err)
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
		if r.ID == "prod" {
			arns[r.ARN] = true
		}
	}

	if len(arns) != 2 {
		t.Fatalf("same-name clusters in two resource groups collapsed to %d distinct ARNs, want 2: %v", len(arns), arns)
	}

	for _, rg := range []string{"rg-a", "rg-b"} {
		found := false
		for arn := range arns {
			if strings.Contains(arn, "resourceGroups/"+rg+"/") {
				found = true
			}
		}

		if !found {
			t.Errorf("no cluster ARN embeds resource group %q: %v", rg, arns)
		}
	}
}
