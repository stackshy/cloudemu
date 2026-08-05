package aks

import (
	"context"
	"testing"
)

func TestClusterTierStoredAndDefaulted(t *testing.T) {
	ctx := context.Background()

	t.Run("explicit tier round-trips", func(t *testing.T) {
		m := newTestMock()
		cluster, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
			Subscription:  "sub-1",
			ResourceGroup: "rg-1",
			Name:          "k8s-standard",
			Location:      "eastus",
			Tier:          "Standard",
		})
		requireNoError(t, err)
		assertEqual(t, "Standard", cluster.Tier)

		got, err := m.GetCluster(ctx, "rg-1", "k8s-standard")
		requireNoError(t, err)
		assertEqual(t, "Standard", got.Tier)
	})

	t.Run("empty tier defaults to Free", func(t *testing.T) {
		m := newTestMock()
		cluster, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
			Subscription:  "sub-1",
			ResourceGroup: "rg-1",
			Name:          "k8s-free",
			Location:      "eastus",
		})
		requireNoError(t, err)
		assertEqual(t, "Free", cluster.Tier)
	})
}

func TestAgentPoolScaleSetPriority(t *testing.T) {
	ctx := context.Background()

	t.Run("spot priority round-trips", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
			Subscription:  "sub-1",
			ResourceGroup: "rg-1",
			Name:          "k8s-spot",
			Location:      "eastus",
		})
		requireNoError(t, err)

		pool, err := m.CreateOrUpdateAgentPool(ctx, "rg-1", "k8s-spot", AgentPoolInput{
			Name:             "spotpool",
			VMSize:           "Standard_D2s_v3",
			Count:            3,
			ScaleSetPriority: "Spot",
		})
		requireNoError(t, err)
		assertEqual(t, "Spot", pool.ScaleSetPriority)

		got, err := m.GetAgentPool(ctx, "rg-1", "k8s-spot", "spotpool")
		requireNoError(t, err)
		assertEqual(t, "Spot", got.ScaleSetPriority)

		pools, err := m.ListAgentPools(ctx, "rg-1", "k8s-spot")
		requireNoError(t, err)
		assertEqual(t, 1, len(pools))
		assertEqual(t, "Spot", pools[0].ScaleSetPriority)
	})

	t.Run("empty priority defaults to Regular", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
			Subscription:  "sub-1",
			ResourceGroup: "rg-1",
			Name:          "k8s-reg",
			Location:      "eastus",
		})
		requireNoError(t, err)

		pool, err := m.CreateOrUpdateAgentPool(ctx, "rg-1", "k8s-reg", AgentPoolInput{
			Name:   "regpool",
			VMSize: "Standard_D2s_v3",
			Count:  2,
		})
		requireNoError(t, err)
		assertEqual(t, "Regular", pool.ScaleSetPriority)
	})
}
