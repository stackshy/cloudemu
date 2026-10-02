package aks

import (
	"context"
	"testing"
)

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	count := int32(1)

	for _, rg := range []string{"Cas1", "cas10"} {
		_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
			Subscription: "s1", ResourceGroup: rg, Name: "aks", Location: "eastus",
			AgentPools: []AgentPoolInput{{Name: "sys", Count: &count, VMSize: "Standard_D2s_v3", Mode: "System"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "cas1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetCluster(ctx, "Cas1", "aks"); err == nil {
		t.Error("cluster in Cas1 survived the purge")
	}

	if pools, _ := m.ListAgentPools(ctx, "Cas1", "aks"); len(pools) != 0 {
		t.Errorf("purged cluster left %d agent pool(s)", len(pools))
	}

	if _, err := m.GetCluster(ctx, "cas10", "aks"); err != nil {
		t.Errorf("cluster in cas10 was purged with cas1: %v", err)
	}
}

// TestPurgeResourceGroupStaysInSubscription: a same-named group in another
// subscription keeps its clusters.
func TestPurgeResourceGroupStaysInSubscription(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	count := int32(1)

	for _, sub := range []string{"sub-a", "sub-b"} {
		_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
			Subscription: sub, ResourceGroup: "shared", Name: "aks-" + sub, Location: "eastus",
			AgentPools: []AgentPoolInput{{Name: "sys", Count: &count, VMSize: "Standard_D2s_v3", Mode: "System"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "SUB-A", "Shared"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetCluster(ctx, "shared", "aks-sub-a"); err == nil {
		t.Error("cluster in sub-a survived its group's purge")
	}

	if _, err := m.GetCluster(ctx, "shared", "aks-sub-b"); err != nil {
		t.Errorf("cluster in sub-b was purged with sub-a's group: %v", err)
	}
}
