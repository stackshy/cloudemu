package aks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// TestSDKAKSAgentPoolProfilesOrderIsStable is a real-user e2e regression: a
// cluster GET's properties.agentPoolProfiles (and a standalone agentPools
// List) must return pools in the same order across repeated reads with no
// intervening writes. ListAgentPools built its result from a Go map without
// sorting, so consecutive real armcontainerservice Get calls against the
// unchanged cluster observed the pools reordered on almost every poll — a
// caller diffing successive reads (e.g. Terraform comparing state) would see
// spurious drift that isn't a real state change.
func TestSDKAKSAgentPoolProfilesOrderIsStable(t *testing.T) {
	clusters, poolsClient, _ := newSDKClients(t)
	ctx := context.Background()

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{Name: to.Ptr("system"), Count: to.Ptr[int32](1), VMSize: to.Ptr("Standard_DS2_v2"),
					Mode: to.Ptr(armcontainerservice.AgentPoolModeSystem)},
			},
		},
	})

	for _, n := range []string{"pzzz", "paaa", "pmmm", "pbbb", "pyyy", "pccc", "pxxx"} {
		poller, err := poolsClient.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", n, armcontainerservice.AgentPool{
			Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
				Count: to.Ptr[int32](1), VMSize: to.Ptr("Standard_DS2_v2"),
				Mode: to.Ptr(armcontainerservice.AgentPoolModeUser),
			},
		}, nil)
		if err != nil {
			t.Fatalf("pool BeginCreateOrUpdate(%s): %v", n, err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("pool PollUntilDone(%s): %v", n, err)
		}
	}

	const wantOrder = "paaa,pbbb,pccc,pmmm,pxxx,pyyy,pzzz,system,"

	for i := 0; i < 10; i++ {
		props := getClusterProps(t, clusters)

		var order string
		for _, p := range props.AgentPoolProfiles {
			order += *p.Name + ","
		}

		if order != wantOrder {
			t.Fatalf("cluster GET %d: agentPoolProfiles order = %q, want %q (unstable ordering)", i, order, wantOrder)
		}

		pager := poolsClient.NewListPager("rg-1", "k8s-1", nil)

		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("pools List %d: %v", i, err)
		}

		var listOrder string
		for _, p := range page.Value {
			listOrder += *p.Name + ","
		}

		if listOrder != wantOrder {
			t.Fatalf("pools List %d: order = %q, want %q (unstable ordering)", i, listOrder, wantOrder)
		}
	}
}

// TestSDKAKSCredentialNamesMatchRealAzure asserts the kubeconfigs[].name field
// returned by List{Admin,User,MonitoringUser}Credentials matches the short
// forms real AKS uses ("clusterAdmin" / "clusterUser" / "clusterMonitoringUser"),
// not the literal ARM action-path segment ("listClusterAdminCredential" etc).
func TestSDKAKSCredentialNamesMatchRealAzure(t *testing.T) {
	clusters, _, _ := newSDKClients(t)
	ctx := context.Background()

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{Location: to.Ptr("eastus")})

	admin, err := clusters.ListClusterAdminCredentials(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("ListClusterAdminCredentials: %v", err)
	}

	assertCredentialName(t, admin.CredentialResults, "clusterAdmin")

	user, err := clusters.ListClusterUserCredentials(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("ListClusterUserCredentials: %v", err)
	}

	assertCredentialName(t, user.CredentialResults, "clusterUser")

	mon, err := clusters.ListClusterMonitoringUserCredentials(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("ListClusterMonitoringUserCredentials: %v", err)
	}

	assertCredentialName(t, mon.CredentialResults, "clusterMonitoringUser")
}

func assertCredentialName(t *testing.T, results armcontainerservice.CredentialResults, want string) {
	t.Helper()

	if len(results.Kubeconfigs) != 1 {
		t.Fatalf("got %d kubeconfigs, want 1", len(results.Kubeconfigs))
	}

	got := results.Kubeconfigs[0].Name
	if got == nil || *got != want {
		t.Fatalf("kubeconfig name: got %v, want %q", got, want)
	}
}
