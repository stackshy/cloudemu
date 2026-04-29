package images_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

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

func TestSDKImageRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Images:          cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := clientOpts(ts)

	// Create a source VM first since CreateImage requires a real instance.
	vmClient, err := armcompute.NewVirtualMachinesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	vmPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "src-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr("Canonical"), Offer: to.Ptr("UbuntuServer"),
						SKU: to.Ptr("22.04-LTS"), Version: to.Ptr("latest"),
					},
				},
				OSProfile: &armcompute.OSProfile{ComputerName: to.Ptr("src"), AdminUsername: to.Ptr("u")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("vm create: %v", err)
	}

	if _, err := vmPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("vm poll: %v", err)
	}

	// Now exercise the images SDK.
	imgClient, err := armcompute.NewImagesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	imgPoller, err := imgClient.BeginCreateOrUpdate(ctx, "rg-1", "img-1",
		armcompute.Image{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.ImageProperties{
				SourceVirtualMachine: &armcompute.SubResource{
					ID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/src-vm"),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("img BeginCreateOrUpdate: %v", err)
	}

	created, err := imgPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("img poll: %v", err)
	}

	if created.Name == nil || *created.Name != "img-1" {
		t.Errorf("name=%v want img-1", created.Name)
	}

	got, err := imgClient.Get(ctx, "rg-1", "img-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "img-1" {
		t.Errorf("got.Name=%v", got.Name)
	}

	pager := imgClient.NewListByResourceGroupPager("rg-1", nil)

	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, im := range page.Value {
			if im.Name != nil && *im.Name == "img-1" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List did not return img-1")
	}

	delPoller, err := imgClient.BeginDelete(ctx, "rg-1", "img-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("delete poll: %v", err)
	}
}
