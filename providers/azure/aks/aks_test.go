package aks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
	)

	return New(opts)
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cluster, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		Subscription:      "sub-1",
		ResourceGroup:     "rg-1",
		Name:              "k8s-prod",
		Location:          "eastus",
		KubernetesVersion: "1.29.5",
		AgentPools: []AgentPoolInput{
			{Name: "system", Count: int32Ptr(2), VMSize: "Standard_D2s_v3", Mode: "System"},
		},
	})
	requireNoError(t, err)

	assertEqual(t, "k8s-prod", cluster.Name)
	assertEqual(t, "rg-1", cluster.ResourceGroup)
	assertEqual(t, "Succeeded", cluster.ProvisioningState)
	assertEqual(t, "Running", cluster.PowerState)
	assertNotEmpty(t, cluster.FQDN)
	assertEqual(t, 1, len(cluster.AgentPoolNames))

	got, err := m.GetCluster(ctx, "rg-1", "k8s-prod")
	requireNoError(t, err)
	assertEqual(t, "1.29.5", got.KubernetesVersion)

	updated, err := m.UpdateClusterTags(ctx, "rg-1", "k8s-prod", map[string]string{"env": "prod"})
	requireNoError(t, err)
	assertEqual(t, "prod", updated.Tags["env"])

	list, err := m.ListClustersByResourceGroup(ctx, "rg-1")
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	requireNoError(t, m.DeleteCluster(ctx, "rg-1", "k8s-prod"))

	if _, err := m.GetCluster(ctx, "rg-1", "k8s-prod"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

// TestListClustersResourceGroupCaseInsensitive verifies a subscription-scoped
// list with a differently-cased resource group still returns the cluster — ARM
// resource-group names are case-insensitive.
func TestListClustersResourceGroupCaseInsensitive(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		Subscription:  "sub-1",
		ResourceGroup: "rg-1",
		Name:          "k8s-prod",
		Location:      "eastus",
		AgentPools:    []AgentPoolInput{{Name: "system", Count: int32Ptr(1), VMSize: "Standard_D2s_v3", Mode: "System"}},
	})
	requireNoError(t, err)

	list, err := m.ListClustersByResourceGroup(ctx, "RG-1")
	requireNoError(t, err)
	assertEqual(t, 1, len(list))
	assertEqual(t, "rg-1", list[0].ResourceGroup)
}

func TestClusterRequiresName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
	}); err == nil {
		t.Fatal("expected error for missing name")
	}

	if _, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		Name: "k8s-1",
	}); err == nil {
		t.Fatal("expected error for missing resource group")
	}
}

func TestAgentPoolLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "k8s-1",
		Location:      "eastus",
	})
	requireNoError(t, err)

	pool, err := m.CreateOrUpdateAgentPool(ctx, "rg-1", "k8s-1", AgentPoolInput{
		Name:   "userpool",
		Count:  int32Ptr(4),
		VMSize: "Standard_D4s_v3",
		Mode:   "User",
	})
	requireNoError(t, err)
	assertEqual(t, int32(4), pool.Count)
	assertEqual(t, "User", pool.Mode)

	// Update pool — count change.
	pool, err = m.CreateOrUpdateAgentPool(ctx, "rg-1", "k8s-1", AgentPoolInput{
		Name:  "userpool",
		Count: int32Ptr(6),
	})
	requireNoError(t, err)
	assertEqual(t, int32(6), pool.Count)

	got, err := m.GetAgentPool(ctx, "rg-1", "k8s-1", "userpool")
	requireNoError(t, err)
	assertEqual(t, int32(6), got.Count)

	pools, err := m.ListAgentPools(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, 1, len(pools))

	requireNoError(t, m.DeleteAgentPool(ctx, "rg-1", "k8s-1", "userpool"))

	if _, err := m.GetAgentPool(ctx, "rg-1", "k8s-1", "userpool"); err == nil {
		t.Fatal("expected NotFound after pool delete")
	}
}

// TestClusterStartStop exercises the power-state lifecycle: stopping a
// running cluster deallocates every agent pool alongside the control plane,
// and starting it again restores both. Both actions are idempotent.
func TestClusterStartStop(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "k8s-1",
		Location:      "eastus",
		AgentPools: []AgentPoolInput{
			{Name: "system", Count: int32Ptr(2), Mode: "System"},
		},
	})
	requireNoError(t, err)

	stopped, err := m.StopCluster(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, "Stopped", stopped.PowerState)
	assertEqual(t, "Succeeded", stopped.ProvisioningState)

	pool, err := m.GetAgentPool(ctx, "rg-1", "k8s-1", "system")
	requireNoError(t, err)
	assertEqual(t, "Stopped", pool.PowerState)

	// Idempotent: stopping an already-stopped cluster still succeeds.
	stopped, err = m.StopCluster(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, "Stopped", stopped.PowerState)

	started, err := m.StartCluster(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, "Running", started.PowerState)

	pool, err = m.GetAgentPool(ctx, "rg-1", "k8s-1", "system")
	requireNoError(t, err)
	assertEqual(t, "Running", pool.PowerState)

	// Idempotent: starting an already-running cluster still succeeds.
	started, err = m.StartCluster(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, "Running", started.PowerState)
}

func TestClusterStartStopRequiresCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.StartCluster(ctx, "rg-1", "ghost"); err == nil {
		t.Fatal("expected NotFound starting a missing cluster")
	}

	if _, err := m.StopCluster(ctx, "rg-1", "ghost"); err == nil {
		t.Fatal("expected NotFound stopping a missing cluster")
	}
}

// TestDeleteAgentPoolRejectsLastSystemPool asserts AKS's invariant that every
// cluster retains at least one System-mode pool: deleting the sole System
// pool is rejected, but deleting a User pool — or a System pool when another
// System pool remains — succeeds.
func TestDeleteAgentPoolRejectsLastSystemPool(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "k8s-1",
		AgentPools: []AgentPoolInput{
			{Name: "system", Count: int32Ptr(2), Mode: "System"},
			{Name: "userpool", Count: int32Ptr(3), Mode: "User"},
		},
	})
	requireNoError(t, err)

	// Deleting the User pool is unaffected.
	requireNoError(t, m.DeleteAgentPool(ctx, "rg-1", "k8s-1", "userpool"))

	// Deleting the last System pool is rejected.
	if err := m.DeleteAgentPool(ctx, "rg-1", "k8s-1", "system"); err == nil {
		t.Fatal("expected error deleting the last System-mode pool")
	}

	if _, err := m.GetAgentPool(ctx, "rg-1", "k8s-1", "system"); err != nil {
		t.Fatalf("system pool should still exist after rejected delete: %v", err)
	}

	// A second System pool makes either one deletable.
	_, err = m.CreateOrUpdateAgentPool(ctx, "rg-1", "k8s-1", AgentPoolInput{
		Name: "system2", Count: int32Ptr(1), Mode: "System",
	})
	requireNoError(t, err)

	requireNoError(t, m.DeleteAgentPool(ctx, "rg-1", "k8s-1", "system"))
}

func TestAgentPoolRequiresCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateOrUpdateAgentPool(ctx, "rg-1", "ghost", AgentPoolInput{
		Name: "userpool",
	}); err == nil {
		t.Fatal("expected NotFound when cluster missing")
	}
}

func TestMaintenanceConfigLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "k8s-1",
	})
	requireNoError(t, err)

	mc, err := m.CreateOrUpdateMaintenanceConfig(ctx, "rg-1", "k8s-1", "default",
		map[string]any{"timeInWeek": []any{}})
	requireNoError(t, err)
	assertEqual(t, "default", mc.Name)

	got, err := m.GetMaintenanceConfig(ctx, "rg-1", "k8s-1", "default")
	requireNoError(t, err)
	assertEqual(t, "default", got.Name)

	configs, err := m.ListMaintenanceConfigs(ctx, "rg-1", "k8s-1")
	requireNoError(t, err)
	assertEqual(t, 1, len(configs))

	requireNoError(t, m.DeleteMaintenanceConfig(ctx, "rg-1", "k8s-1", "default"))

	if _, err := m.GetMaintenanceConfig(ctx, "rg-1", "k8s-1", "default"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestDeleteClusterCascades(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "k8s-1",
		AgentPools: []AgentPoolInput{
			{Name: "system", Count: int32Ptr(2)},
		},
	})
	requireNoError(t, err)

	_, err = m.CreateOrUpdateMaintenanceConfig(ctx, "rg-1", "k8s-1", "default",
		map[string]any{})
	requireNoError(t, err)

	requireNoError(t, m.DeleteCluster(ctx, "rg-1", "k8s-1"))

	pools, err := m.ListAgentPools(ctx, "rg-1", "k8s-1")
	if err == nil && len(pools) != 0 {
		t.Fatalf("expected pools removed on cluster delete, got %d", len(pools))
	}

	if _, err := m.GetMaintenanceConfig(ctx, "rg-1", "k8s-1", "default"); err == nil {
		t.Fatal("expected maintenance config removed on cluster delete")
	}
}

func TestRotateClusterCertificates(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{
		ResourceGroup: "rg-1",
		Name:          "k8s-1",
	})
	requireNoError(t, err)

	requireNoError(t, m.RotateClusterCertificates(ctx, "rg-1", "k8s-1"))

	if err := m.RotateClusterCertificates(ctx, "rg-1", "missing"); err == nil {
		t.Fatal("expected NotFound on rotate for missing cluster")
	}
}

func TestKubeconfigDataPlaneSentinel(t *testing.T) {
	m := newTestMock()
	kc := m.Kubeconfig("rg-1", "k8s-1")

	if !strings.Contains(string(kc), "AKS-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected stub kubeconfig to mention the not-implemented sentinel, got: %s", string(kc))
	}
}

func TestListClustersAcrossResourceGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateOrUpdateCluster(ctx, ClusterInput{ResourceGroup: "rg-a", Name: "c1"})
	requireNoError(t, err)
	_, err = m.CreateOrUpdateCluster(ctx, ClusterInput{ResourceGroup: "rg-b", Name: "c2"})
	requireNoError(t, err)

	all, err := m.ListClusters(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(all))

	rgA, err := m.ListClustersByResourceGroup(ctx, "rg-a")
	requireNoError(t, err)
	assertEqual(t, 1, len(rgA))
}

// Hand-rolled helpers per CLAUDE.md.

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func int32Ptr(v int32) *int32 {
	return &v
}
