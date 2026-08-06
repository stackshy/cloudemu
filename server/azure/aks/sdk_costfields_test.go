package aks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// TestSDKAKSCostFieldsRoundTrip asserts that the cost-sensitive inputs a
// discoverer reads — the cluster SKU tier and the agent-pool scale-set priority
// — survive a real armcontainerservice create/GET round-trip instead of being
// dropped and reset to the Free / Regular defaults.
func TestSDKAKSCostFieldsRoundTrip(t *testing.T) {
	clusters, pools, _ := newSDKClients(t)
	ctx := context.Background()

	// Create a cluster with sku.tier=Standard and an inline Spot pool.
	cPoller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		SKU: &armcontainerservice.ManagedClusterSKU{
			Name: to.Ptr(armcontainerservice.ManagedClusterSKUNameBase),
			Tier: to.Ptr(armcontainerservice.ManagedClusterSKUTierStandard),
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:             to.Ptr("spotinline"),
					Count:            to.Ptr[int32](2),
					VMSize:           to.Ptr("Standard_DS2_v2"),
					Mode:             to.Ptr(armcontainerservice.AgentPoolModeUser),
					ScaleSetPriority: to.Ptr(armcontainerservice.ScaleSetPrioritySpot),
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Cluster BeginCreateOrUpdate: %v", err)
	}

	createResp, err := cPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Cluster PollUntilDone: %v", err)
	}

	// The create response echoes the tier back (not the Free default).
	if createResp.SKU == nil || createResp.SKU.Tier == nil ||
		*createResp.SKU.Tier != armcontainerservice.ManagedClusterSKUTierStandard {
		t.Fatalf("create: got sku.tier %v, want Standard", skuTier(createResp.ManagedCluster))
	}

	// GET reads the tier back.
	got, err := clusters.Get(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("Cluster Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Tier == nil ||
		*got.SKU.Tier != armcontainerservice.ManagedClusterSKUTierStandard {
		t.Fatalf("get: got sku.tier %v, want Standard", skuTier(got.ManagedCluster))
	}

	// The inline pool carries scaleSetPriority=Spot (not the Regular default).
	inlinePool, err := pools.Get(ctx, "rg-1", "k8s-1", "spotinline", nil)
	if err != nil {
		t.Fatalf("inline pool Get: %v", err)
	}

	if p := inlinePool.Properties; p == nil || p.ScaleSetPriority == nil ||
		*p.ScaleSetPriority != armcontainerservice.ScaleSetPrioritySpot {
		t.Fatalf("inline pool: got scaleSetPriority %v, want Spot", poolPriority(inlinePool.AgentPool))
	}

	// A standalone agent-pool create also carries scaleSetPriority through.
	poolPoller, err := pools.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", "spotpool", armcontainerservice.AgentPool{
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Count:            to.Ptr[int32](3),
			VMSize:           to.Ptr("Standard_D4s_v3"),
			Mode:             to.Ptr(armcontainerservice.AgentPoolModeUser),
			ScaleSetPriority: to.Ptr(armcontainerservice.ScaleSetPrioritySpot),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Pool BeginCreateOrUpdate: %v", err)
	}

	poolResp, err := poolPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Pool PollUntilDone: %v", err)
	}

	if p := poolResp.Properties; p == nil || p.ScaleSetPriority == nil ||
		*p.ScaleSetPriority != armcontainerservice.ScaleSetPrioritySpot {
		t.Fatalf("create pool: got scaleSetPriority %v, want Spot", poolPriority(poolResp.AgentPool))
	}

	gotPool, err := pools.Get(ctx, "rg-1", "k8s-1", "spotpool", nil)
	if err != nil {
		t.Fatalf("Pool Get: %v", err)
	}

	if p := gotPool.Properties; p == nil || p.ScaleSetPriority == nil ||
		*p.ScaleSetPriority != armcontainerservice.ScaleSetPrioritySpot {
		t.Fatalf("get pool: got scaleSetPriority %v, want Spot", poolPriority(gotPool.AgentPool))
	}
}

func skuTier(c armcontainerservice.ManagedCluster) any {
	if c.SKU == nil || c.SKU.Tier == nil {
		return nil
	}

	return *c.SKU.Tier
}

func poolPriority(p armcontainerservice.AgentPool) any {
	if p.Properties == nil || p.Properties.ScaleSetPriority == nil {
		return nil
	}

	return *p.Properties.ScaleSetPriority
}
