package aks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

func TestSDKAKSClusterIdentityAndDefaults(t *testing.T) {
	clusters, _, _ := newSDKClients(t)
	ctx := context.Background()

	poller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Identity: &armcontainerservice.ManagedClusterIdentity{
			Type: to.Ptr(armcontainerservice.ResourceIdentityTypeSystemAssigned),
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			KubernetesVersion: to.Ptr("1.30.2"),
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:    to.Ptr("system"),
					Count:   to.Ptr[int32](1),
					VMSize:  to.Ptr("Standard_DS2_v2"),
					Mode:    to.Ptr(armcontainerservice.AgentPoolModeSystem),
					MaxPods: to.Ptr[int32](110),
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	props := resp.ManagedCluster.Properties
	if props == nil {
		t.Fatal("nil properties")
	}

	// F3: system-assigned identity echoed with principal/tenant.
	if resp.Identity == nil || resp.Identity.PrincipalID == nil || *resp.Identity.PrincipalID == "" {
		t.Fatal("expected identity principalId")
	}

	if resp.Identity.TenantID == nil || *resp.Identity.TenantID == "" {
		t.Fatal("expected identity tenantId")
	}

	// F6: defaults synthesized server-side.
	if props.EnableRBAC == nil || !*props.EnableRBAC {
		t.Fatal("expected enableRBAC true default")
	}

	if props.CurrentKubernetesVersion == nil || *props.CurrentKubernetesVersion != "1.30.2" {
		t.Fatalf("got currentKubernetesVersion %v, want 1.30.2", props.CurrentKubernetesVersion)
	}

	if props.NetworkProfile == nil || props.NetworkProfile.NetworkPlugin == nil {
		t.Fatal("expected networkProfile with networkPlugin default")
	}

	// F7 + F8 + F12: pool completeness, submitted maxPods preserved, version inherited.
	if len(props.AgentPoolProfiles) != 1 {
		t.Fatalf("got %d pools, want 1", len(props.AgentPoolProfiles))
	}

	pool := props.AgentPoolProfiles[0]
	if pool.MaxPods == nil || *pool.MaxPods != 110 {
		t.Fatalf("got maxPods %v, want 110", pool.MaxPods)
	}

	if pool.Type == nil || *pool.Type != armcontainerservice.AgentPoolTypeVirtualMachineScaleSets {
		t.Fatalf("got pool type %v, want VirtualMachineScaleSets", pool.Type)
	}

	if pool.OSDiskType == nil || *pool.OSDiskType == "" {
		t.Fatal("expected osDiskType populated")
	}

	if pool.PowerState == nil || pool.PowerState.Code == nil {
		t.Fatal("expected pool powerState populated")
	}

	if pool.NodeImageVersion == nil || *pool.NodeImageVersion == "" {
		t.Fatal("expected nodeImageVersion populated")
	}

	if pool.OrchestratorVersion == nil || *pool.OrchestratorVersion != "1.30.2" {
		t.Fatalf("got pool orchestratorVersion %v, want inherited 1.30.2", pool.OrchestratorVersion)
	}
}
