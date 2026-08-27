package aks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// TestSDKAKSClusterPUTPreservesStandalonePool is the critical data-loss guard:
// a cluster PUT that omits agentPoolProfiles must NOT wipe pools created via the
// standalone agentPools API, and must preserve networkProfile + identity. The
// old replaceInlinePools wiped every pool under the cluster, dropping the
// standalone user pool and resetting the cluster to zero pools.
func TestSDKAKSClusterPUTPreservesStandalonePool(t *testing.T) {
	clusters, pools, _ := newSDKClients(t)
	ctx := context.Background()

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Identity: &armcontainerservice.ManagedClusterIdentity{
			Type: to.Ptr(armcontainerservice.ResourceIdentityTypeSystemAssigned),
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			KubernetesVersion: to.Ptr("1.29.0"),
			NetworkProfile: &armcontainerservice.NetworkProfile{
				NetworkPlugin: to.Ptr(armcontainerservice.NetworkPluginAzure),
				ServiceCidr:   to.Ptr("10.50.0.0/16"),
			},
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:   to.Ptr("system"),
					Count:  to.Ptr[int32](1),
					VMSize: to.Ptr("Standard_DS2_v2"),
					Mode:   to.Ptr(armcontainerservice.AgentPoolModeSystem),
				},
			},
		},
	})

	// Add a standalone user pool (azurerm_kubernetes_cluster_node_pool).
	poolPoller, err := pools.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", "userpool", armcontainerservice.AgentPool{
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Count:  to.Ptr[int32](2),
			VMSize: to.Ptr("Standard_D4s_v3"),
			Mode:   to.Ptr(armcontainerservice.AgentPoolModeUser),
		},
	}, nil)
	if err != nil {
		t.Fatalf("pool BeginCreateOrUpdate: %v", err)
	}

	if _, err := poolPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool PollUntilDone: %v", err)
	}

	// PUT the cluster bumping ONLY kubernetesVersion — no agentPoolProfiles, no
	// networkProfile, no identity. This is the version-only full PUT that used
	// to wipe every pool.
	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			KubernetesVersion: to.Ptr("1.30.0"),
		},
	})

	pager := pools.NewListPager("rg-1", "k8s-1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("pool List: %v", err)
	}

	if len(page.Value) != 2 {
		t.Fatalf("got %d pools after cluster PUT, want 2 (data-loss regression)", len(page.Value))
	}

	assertVersionAndNetworkPreserved(t, getClusterProps(t, clusters))
	assertIdentityPreserved(t, clusters)
}

func assertVersionAndNetworkPreserved(t *testing.T, props *armcontainerservice.ManagedClusterProperties) {
	t.Helper()

	if props.KubernetesVersion == nil || *props.KubernetesVersion != "1.30.0" {
		t.Fatalf("kubernetesVersion: got %v, want 1.30.0", props.KubernetesVersion)
	}

	if props.NetworkProfile == nil || props.NetworkProfile.ServiceCidr == nil ||
		*props.NetworkProfile.ServiceCidr != "10.50.0.0/16" {
		t.Fatalf("networkProfile.serviceCidr not preserved: %+v", props.NetworkProfile)
	}
}

func assertIdentityPreserved(t *testing.T, clusters *armcontainerservice.ManagedClustersClient) {
	t.Helper()

	got, err := clusters.Get(context.Background(), "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity == nil || got.Identity.Type == nil ||
		*got.Identity.Type != armcontainerservice.ResourceIdentityTypeSystemAssigned {
		t.Fatalf("identity not preserved: %+v", got.Identity)
	}

	if got.Identity.PrincipalID == nil || *got.Identity.PrincipalID == "" {
		t.Fatal("expected preserved system-assigned principalId")
	}
}

// TestSDKAKSClusterPUTNilPropertiesPreservesFields asserts a cluster PUT that
// omits the properties block entirely (a tags-only full PUT) preserves the
// stored kubernetesVersion, dnsPrefix, and networkProfile instead of resetting
// them to defaults.
func TestSDKAKSClusterPUTNilPropertiesPreservesFields(t *testing.T) {
	clusters, _, _ := newSDKClients(t)

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			KubernetesVersion: to.Ptr("1.29.3"),
			DNSPrefix:         to.Ptr("custom-dns"),
			NetworkProfile: &armcontainerservice.NetworkProfile{
				NetworkPlugin: to.Ptr(armcontainerservice.NetworkPluginAzure),
				ServiceCidr:   to.Ptr("10.77.0.0/16"),
			},
		},
	})

	// Full PUT carrying only tags — no properties block at all.
	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"team": to.Ptr("infra")},
	})

	props := getClusterProps(t, clusters)

	if props.KubernetesVersion == nil || *props.KubernetesVersion != "1.29.3" {
		t.Fatalf("kubernetesVersion reset: got %v, want 1.29.3", props.KubernetesVersion)
	}

	if props.DNSPrefix == nil || *props.DNSPrefix != "custom-dns" {
		t.Fatalf("dnsPrefix reset: got %v, want custom-dns", props.DNSPrefix)
	}

	if props.NetworkProfile == nil || props.NetworkProfile.ServiceCidr == nil ||
		*props.NetworkProfile.ServiceCidr != "10.77.0.0/16" {
		t.Fatalf("networkProfile reset: %+v", props.NetworkProfile)
	}
}
