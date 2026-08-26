package aks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

const testSubnetID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
	"Microsoft.Network/virtualNetworks/vnet-1/subnets/nodes"

// createDriftCluster PUTs a cluster and blocks until the LRO settles. Shared by
// the Terraform-drift round-trip tests so each stays under the length limit.
func createDriftCluster(t *testing.T, c *armcontainerservice.ManagedClustersClient, mc armcontainerservice.ManagedCluster) {
	t.Helper()

	ctx := context.Background()

	poller, err := c.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", mc, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
}

// getClusterProps GETs the cluster and returns its non-nil properties.
func getClusterProps(t *testing.T, c *armcontainerservice.ManagedClustersClient) *armcontainerservice.ManagedClusterProperties {
	t.Helper()

	got, err := c.Get(context.Background(), "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil {
		t.Fatal("nil properties on GET")
	}

	return got.Properties
}

// TestSDKAKSNetworkProfileRoundTrips is B1: a submitted networkProfile is echoed
// back verbatim on GET, instead of being replaced by fabricated defaults.
func TestSDKAKSNetworkProfileRoundTrips(t *testing.T) {
	clusters, _, _ := newSDKClients(t)

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			NetworkProfile: &armcontainerservice.NetworkProfile{
				NetworkPlugin: to.Ptr(armcontainerservice.NetworkPluginAzure),
				ServiceCidr:   to.Ptr("10.99.0.0/16"),
				DNSServiceIP:  to.Ptr("10.99.0.10"),
			},
		},
	})

	np := getClusterProps(t, clusters).NetworkProfile
	if np == nil {
		t.Fatal("expected networkProfile on GET")
	}

	if np.NetworkPlugin == nil || *np.NetworkPlugin != armcontainerservice.NetworkPluginAzure {
		t.Fatalf("networkPlugin: got %v, want azure", np.NetworkPlugin)
	}

	if np.ServiceCidr == nil || *np.ServiceCidr != "10.99.0.0/16" {
		t.Fatalf("serviceCidr: got %v, want 10.99.0.0/16", np.ServiceCidr)
	}

	if np.DNSServiceIP == nil || *np.DNSServiceIP != "10.99.0.10" {
		t.Fatalf("dnsServiceIP: got %v, want 10.99.0.10", np.DNSServiceIP)
	}

	// Sub-keys the caller never sent must not be fabricated.
	if np.PodCidr != nil {
		t.Fatalf("podCidr fabricated: %v", *np.PodCidr)
	}
}

// TestSDKAKSNetworkProfileDefaultWhenOmitted is B1-default: omitting
// networkProfile yields the real AKS defaults (kubenet / 10.0.0.0/16 / .10).
func TestSDKAKSNetworkProfileDefaultWhenOmitted(t *testing.T) {
	clusters, _, _ := newSDKClients(t)

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
	})

	np := getClusterProps(t, clusters).NetworkProfile
	if np == nil || np.NetworkPlugin == nil {
		t.Fatal("expected default networkProfile with networkPlugin")
	}

	if *np.NetworkPlugin != armcontainerservice.NetworkPluginKubenet {
		t.Fatalf("networkPlugin: got %v, want kubenet", *np.NetworkPlugin)
	}

	if np.ServiceCidr == nil || *np.ServiceCidr != "10.0.0.0/16" {
		t.Fatalf("serviceCidr: got %v, want 10.0.0.0/16", np.ServiceCidr)
	}

	if np.DNSServiceIP == nil || *np.DNSServiceIP != "10.0.0.10" {
		t.Fatalf("dnsServiceIP: got %v, want 10.0.0.10", np.DNSServiceIP)
	}
}

// TestSDKAKSInlineAgentPoolAdvancedFields is B2: the advanced fields Terraform's
// default_node_pool submits inline in the cluster PUT survive to GET.
func TestSDKAKSInlineAgentPoolAdvancedFields(t *testing.T) {
	clusters, _, _ := newSDKClients(t)

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:              to.Ptr("system"),
					Count:             to.Ptr[int32](1),
					VMSize:            to.Ptr("Standard_DS2_v2"),
					Mode:              to.Ptr(armcontainerservice.AgentPoolModeSystem),
					AvailabilityZones: []*string{to.Ptr("1"), to.Ptr("2"), to.Ptr("3")},
					EnableAutoScaling: to.Ptr(true),
					MinCount:          to.Ptr[int32](1),
					MaxCount:          to.Ptr[int32](5),
					VnetSubnetID:      to.Ptr(testSubnetID),
					OSSKU:             to.Ptr(armcontainerservice.OSSKUAzureLinux),
				},
			},
		},
	})

	pools := getClusterProps(t, clusters).AgentPoolProfiles
	if len(pools) != 1 {
		t.Fatalf("got %d pools, want 1", len(pools))
	}

	assertAdvancedProfile(t, pools[0])
}

// TestSDKAKSStandaloneAgentPoolAdvancedFields is the regression guard: the
// standalone agentPools PUT→GET path still round-trips the same advanced fields.
func TestSDKAKSStandaloneAgentPoolAdvancedFields(t *testing.T) {
	clusters, poolsClient, _ := newSDKClients(t)
	ctx := context.Background()

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{Location: to.Ptr("eastus")})

	poller, err := poolsClient.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", "userpool", armcontainerservice.AgentPool{
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Count:             to.Ptr[int32](1),
			VMSize:            to.Ptr("Standard_DS2_v2"),
			Mode:              to.Ptr(armcontainerservice.AgentPoolModeUser),
			AvailabilityZones: []*string{to.Ptr("1"), to.Ptr("2"), to.Ptr("3")},
			EnableAutoScaling: to.Ptr(true),
			MinCount:          to.Ptr[int32](1),
			MaxCount:          to.Ptr[int32](5),
			VnetSubnetID:      to.Ptr(testSubnetID),
			OSSKU:             to.Ptr(armcontainerservice.OSSKUAzureLinux),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Pool BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Pool PollUntilDone: %v", err)
	}

	got, err := poolsClient.Get(ctx, "rg-1", "k8s-1", "userpool", nil)
	if err != nil {
		t.Fatalf("Pool Get: %v", err)
	}

	assertAdvancedPoolProps(t, got.Properties)
}

// TestSDKAKSServicePrincipalSecretStripped is the #715 regression: the write-only
// servicePrincipalProfile.secret is never echoed, while clientId round-trips.
func TestSDKAKSServicePrincipalSecretStripped(t *testing.T) {
	clusters, _, _ := newSDKClients(t)

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			ServicePrincipalProfile: &armcontainerservice.ManagedClusterServicePrincipalProfile{
				ClientID: to.Ptr("client-id-123"),
				Secret:   to.Ptr("super-secret-value"),
			},
		},
	})

	spp := getClusterProps(t, clusters).ServicePrincipalProfile
	if spp != nil && spp.Secret != nil {
		t.Fatalf("GET leaked write-only servicePrincipalProfile.secret: %v", *spp.Secret)
	}

	if spp == nil || spp.ClientID == nil || *spp.ClientID != "client-id-123" {
		t.Fatalf("clientId did not round-trip: %v", spp)
	}
}

// assertAdvancedProfile checks the inline-profile advanced fields.
func assertAdvancedProfile(t *testing.T, p *armcontainerservice.ManagedClusterAgentPoolProfile) {
	t.Helper()

	assertZones(t, p.AvailabilityZones)

	if p.EnableAutoScaling == nil || !*p.EnableAutoScaling {
		t.Fatalf("enableAutoScaling: got %v, want true", p.EnableAutoScaling)
	}

	assertBounds(t, p.MinCount, p.MaxCount)
	assertSubnetAndSKU(t, p.VnetSubnetID, p.OSSKU)
}

// assertAdvancedPoolProps checks the standalone-pool advanced fields.
func assertAdvancedPoolProps(t *testing.T, p *armcontainerservice.ManagedClusterAgentPoolProfileProperties) {
	t.Helper()

	if p == nil {
		t.Fatal("nil pool properties")
	}

	assertZones(t, p.AvailabilityZones)

	if p.EnableAutoScaling == nil || !*p.EnableAutoScaling {
		t.Fatalf("enableAutoScaling: got %v, want true", p.EnableAutoScaling)
	}

	assertBounds(t, p.MinCount, p.MaxCount)
	assertSubnetAndSKU(t, p.VnetSubnetID, p.OSSKU)
}

func assertZones(t *testing.T, zones []*string) {
	t.Helper()

	want := []string{"1", "2", "3"}
	if len(zones) != len(want) {
		t.Fatalf("availabilityZones: got %d, want %d", len(zones), len(want))
	}

	for i, z := range zones {
		if z == nil || *z != want[i] {
			t.Fatalf("availabilityZones[%d]: got %v, want %s", i, z, want[i])
		}
	}
}

func assertBounds(t *testing.T, minCount, maxCount *int32) {
	t.Helper()

	if minCount == nil || *minCount != 1 {
		t.Fatalf("minCount: got %v, want 1", minCount)
	}

	if maxCount == nil || *maxCount != 5 {
		t.Fatalf("maxCount: got %v, want 5", maxCount)
	}
}

func assertSubnetAndSKU(t *testing.T, vnetSubnetID *string, osSKU *armcontainerservice.OSSKU) {
	t.Helper()

	if vnetSubnetID == nil || *vnetSubnetID != testSubnetID {
		t.Fatalf("vnetSubnetID: got %v, want %s", vnetSubnetID, testSubnetID)
	}

	if osSKU == nil || *osSKU != armcontainerservice.OSSKUAzureLinux {
		t.Fatalf("osSKU: got %v, want AzureLinux", osSKU)
	}
}
