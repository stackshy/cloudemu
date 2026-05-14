package aks_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newSDKClients(t *testing.T) (
	*armcontainerservice.ManagedClustersClient,
	*armcontainerservice.AgentPoolsClient,
	*armcontainerservice.MaintenanceConfigurationsClient,
) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{AKS: cloudP.AKS})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armcontainerservice.NewClientFactory("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return cf.NewManagedClustersClient(), cf.NewAgentPoolsClient(), cf.NewMaintenanceConfigurationsClient()
}

func TestSDKAKSManagedClusterLifecycle(t *testing.T) {
	clusters, _, _ := newSDKClients(t)
	ctx := context.Background()

	poller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			KubernetesVersion: to.Ptr("1.29.5"),
			DNSPrefix:         to.Ptr("k8s-1-dns"),
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:   to.Ptr("system"),
					Count:  to.Ptr[int32](2),
					VMSize: to.Ptr("Standard_DS2_v2"),
					Mode:   to.Ptr(armcontainerservice.AgentPoolModeSystem),
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

	if resp.ManagedCluster.Name == nil || *resp.ManagedCluster.Name != "k8s-1" {
		t.Fatalf("got name %v, want k8s-1", resp.ManagedCluster.Name)
	}

	if resp.ManagedCluster.Properties == nil || resp.ManagedCluster.Properties.Fqdn == nil ||
		*resp.ManagedCluster.Properties.Fqdn == "" {
		t.Fatal("expected Fqdn populated on response")
	}

	got, err := clusters.Get(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.ManagedCluster.Properties.KubernetesVersion != "1.29.5" {
		t.Fatalf("got k8s version %q, want 1.29.5", *got.ManagedCluster.Properties.KubernetesVersion)
	}

	// PATCH (update tags).
	patchPoller, err := clusters.BeginUpdateTags(ctx, "rg-1", "k8s-1", armcontainerservice.TagsObject{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdateTags: %v", err)
	}

	patchResp, err := patchPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("UpdateTags PollUntilDone: %v", err)
	}

	if val := patchResp.ManagedCluster.Tags["env"]; val == nil || *val != "prod" {
		t.Fatalf("expected env=prod tag after PATCH")
	}

	// List under resource group.
	pager := clusters.NewListByResourceGroupPager("rg-1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d clusters, want 1", len(page.Value))
	}

	// Subscription-wide list.
	subPager := clusters.NewListPager(nil)

	subPage, err := subPager.NextPage(ctx)
	if err != nil {
		t.Fatalf("subscription List: %v", err)
	}

	if len(subPage.Value) != 1 {
		t.Fatalf("got %d clusters across subscription, want 1", len(subPage.Value))
	}

	// Rotate certs.
	rotPoller, err := clusters.BeginRotateClusterCertificates(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("BeginRotateClusterCertificates: %v", err)
	}

	if _, err := rotPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Rotate PollUntilDone: %v", err)
	}

	// Delete.
	delPoller, err := clusters.BeginDelete(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	if _, err := clusters.Get(ctx, "rg-1", "k8s-1", nil); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestSDKAKSAgentPoolLifecycle(t *testing.T) {
	clusters, pools, _ := newSDKClients(t)
	ctx := context.Background()

	// Create cluster first.
	cPoller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Cluster BeginCreateOrUpdate: %v", err)
	}

	if _, err := cPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Cluster PollUntilDone: %v", err)
	}

	// Create pool.
	poolPoller, err := pools.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", "userpool", armcontainerservice.AgentPool{
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Count:  to.Ptr[int32](4),
			VMSize: to.Ptr("Standard_D4s_v3"),
			Mode:   to.Ptr(armcontainerservice.AgentPoolModeUser),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Pool BeginCreateOrUpdate: %v", err)
	}

	poolResp, err := poolPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Pool PollUntilDone: %v", err)
	}

	if poolResp.AgentPool.Name == nil || *poolResp.AgentPool.Name != "userpool" {
		t.Fatalf("got pool name %v, want userpool", poolResp.AgentPool.Name)
	}

	if *poolResp.AgentPool.Properties.Count != 4 {
		t.Fatalf("got count %d, want 4", *poolResp.AgentPool.Properties.Count)
	}

	// Get.
	got, err := pools.Get(ctx, "rg-1", "k8s-1", "userpool", nil)
	if err != nil {
		t.Fatalf("Pool Get: %v", err)
	}

	if *got.AgentPool.Properties.VMSize != "Standard_D4s_v3" {
		t.Fatalf("got vmSize %q, want Standard_D4s_v3", *got.AgentPool.Properties.VMSize)
	}

	// List.
	pager := pools.NewListPager("rg-1", "k8s-1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("Pool List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d pools, want 1", len(page.Value))
	}

	// Delete.
	delPoller, err := pools.BeginDelete(ctx, "rg-1", "k8s-1", "userpool", nil)
	if err != nil {
		t.Fatalf("Pool BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Pool delete poll: %v", err)
	}

	if _, err := pools.Get(ctx, "rg-1", "k8s-1", "userpool", nil); err == nil {
		t.Fatal("expected NotFound after pool delete")
	}
}

func TestSDKAKSMaintenanceConfigLifecycle(t *testing.T) {
	clusters, _, mc := newSDKClients(t)
	ctx := context.Background()

	cPoller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Cluster BeginCreateOrUpdate: %v", err)
	}

	if _, err := cPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Cluster PollUntilDone: %v", err)
	}

	created, err := mc.CreateOrUpdate(ctx, "rg-1", "k8s-1", "default", armcontainerservice.MaintenanceConfiguration{
		Properties: &armcontainerservice.MaintenanceConfigurationProperties{
			TimeInWeek: []*armcontainerservice.TimeInWeek{},
		},
	}, nil)
	if err != nil {
		t.Fatalf("MaintenanceConfig CreateOrUpdate: %v", err)
	}

	if *created.MaintenanceConfiguration.Name != "default" {
		t.Fatalf("got name %q, want default", *created.MaintenanceConfiguration.Name)
	}

	if _, err := mc.Get(ctx, "rg-1", "k8s-1", "default", nil); err != nil {
		t.Fatalf("MaintenanceConfig Get: %v", err)
	}

	pager := mc.NewListByManagedClusterPager("rg-1", "k8s-1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("MaintenanceConfig List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d maintenance configs, want 1", len(page.Value))
	}

	if _, err := mc.Delete(ctx, "rg-1", "k8s-1", "default", nil); err != nil {
		t.Fatalf("MaintenanceConfig Delete: %v", err)
	}

	if _, err := mc.Get(ctx, "rg-1", "k8s-1", "default", nil); err == nil {
		t.Fatal("expected NotFound after maintenance config delete")
	}
}

func TestSDKAKSListClusterAdminCredential(t *testing.T) {
	clusters, _, _ := newSDKClients(t)
	ctx := context.Background()

	cPoller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Cluster BeginCreateOrUpdate: %v", err)
	}

	if _, err := cPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Cluster PollUntilDone: %v", err)
	}

	resp, err := clusters.ListClusterAdminCredentials(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("ListClusterAdminCredentials: %v", err)
	}

	if len(resp.CredentialResults.Kubeconfigs) != 1 {
		t.Fatalf("got %d kubeconfigs, want 1", len(resp.CredentialResults.Kubeconfigs))
	}

	value := resp.CredentialResults.Kubeconfigs[0].Value
	if !strings.Contains(string(value), "AKS-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected stub kubeconfig to mention not-implemented sentinel; got: %s", string(value))
	}

	userResp, err := clusters.ListClusterUserCredentials(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("ListClusterUserCredentials: %v", err)
	}

	if len(userResp.CredentialResults.Kubeconfigs) != 1 {
		t.Fatalf("user kubeconfigs: got %d, want 1", len(userResp.CredentialResults.Kubeconfigs))
	}

	monResp, err := clusters.ListClusterMonitoringUserCredentials(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("ListClusterMonitoringUserCredentials: %v", err)
	}

	if len(monResp.CredentialResults.Kubeconfigs) != 1 {
		t.Fatalf("monitoring kubeconfigs: got %d, want 1", len(monResp.CredentialResults.Kubeconfigs))
	}
}
