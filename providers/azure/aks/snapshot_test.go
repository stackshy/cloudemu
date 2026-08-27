package aks

import (
	"context"
	"testing"
)

func TestSnapshotRoundTripAKS(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateOrUpdateCluster(ctx, ClusterInput{
		Subscription:      "sub-1",
		ResourceGroup:     "rg-1",
		Name:              "k8s-1",
		Location:          "eastus",
		KubernetesVersion: "1.29.5",
		AgentPools: []AgentPoolInput{
			{Name: "system", Count: int32Ptr(2), VMSize: "Standard_D2s_v3", Mode: "System"},
		},
	})
	requireNoError(t, err)

	_, err = src.CreateOrUpdateAgentPool(ctx, "rg-1", "k8s-1", AgentPoolInput{
		Name: "user", Count: int32Ptr(1), VMSize: "Standard_D2s_v3", Mode: "User",
	})
	requireNoError(t, err)

	_, err = src.CreateOrUpdateMaintenanceConfig(ctx, "rg-1", "k8s-1", "default",
		map[string]any{"timeInWeek": []any{}})
	requireNoError(t, err)

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	cluster, err := dst.GetCluster(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, "k8s-1", cluster.Name)

	pool, err := dst.GetAgentPool(ctx, "rg-1", "k8s-1", "user")
	requireNoError(t, err)
	assertEqual(t, "user", pool.Name)

	mc, err := dst.GetMaintenanceConfig(ctx, "rg-1", "k8s-1", "default")
	requireNoError(t, err)
	assertEqual(t, "default", mc.Name)
}
