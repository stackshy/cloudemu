package network_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func clientOpts(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

func TestSDKVNetRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	vnetClient, err := armnetwork.NewVirtualNetworksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	poller, err := vnetClient.BeginCreateOrUpdate(ctx, "rg-1", "vnet-1",
		armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("vnet BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("vnet poll: %v", err)
	}

	if created.Name == nil || *created.Name != "vnet-1" {
		t.Errorf("name=%v want vnet-1", created.Name)
	}

	got, err := vnetClient.Get(ctx, "rg-1", "vnet-1", nil)
	if err != nil {
		t.Fatalf("vnet Get: %v", err)
	}

	if got.Name == nil || *got.Name != "vnet-1" {
		t.Errorf("got.Name=%v", got.Name)
	}

	pager := vnetClient.NewListPager("rg-1", nil)

	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("vnet list: %v", err)
		}

		for _, v := range page.Value {
			if v.Name != nil && *v.Name == "vnet-1" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List did not return vnet-1")
	}

	delPoller, err := vnetClient.BeginDelete(ctx, "rg-1", "vnet-1", nil)
	if err != nil {
		t.Fatalf("vnet BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("vnet delete poll: %v", err)
	}
}

func TestSDKNSGRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Network: cloudP.VNet})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	nsgClient, err := armnetwork.NewSecurityGroupsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	poller, err := nsgClient.BeginCreateOrUpdate(ctx, "rg-1", "nsg-1",
		armnetwork.SecurityGroup{
			Location: to.Ptr("eastus"),
		}, nil)
	if err != nil {
		t.Fatalf("nsg BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("nsg poll: %v", err)
	}

	got, err := nsgClient.Get(ctx, "rg-1", "nsg-1", nil)
	if err != nil {
		t.Fatalf("nsg Get: %v", err)
	}

	if got.Name == nil || *got.Name != "nsg-1" {
		t.Errorf("got.Name=%v", got.Name)
	}

	delPoller, err := nsgClient.BeginDelete(ctx, "rg-1", "nsg-1", nil)
	if err != nil {
		t.Fatalf("nsg BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("nsg delete poll: %v", err)
	}
}
