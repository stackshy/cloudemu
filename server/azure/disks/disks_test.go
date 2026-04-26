package disks_test

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

// newDisksClient builds a real armcompute.DisksClient pointing at the test
// server. The Azure SDK rejects bearer tokens over plain HTTP, so we use TLS.
func newDisksClient(t *testing.T, ts *httptest.Server) *armcompute.DisksClient {
	t.Helper()

	c := cloud.Configuration{
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
			Cloud:     c,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armcompute.NewDisksClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func TestSDKDiskRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newDisksClient(t, ts)
	ctx := context.Background()

	createPoller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "data-disk-1",
		armcompute.Disk{
			Location: to.Ptr("eastus"),
			SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
			Properties: &armcompute.DiskProperties{
				CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
				DiskSizeGB:   to.Ptr[int32](128),
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := createPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if created.Name == nil || *created.Name != "data-disk-1" {
		t.Errorf("name=%v want data-disk-1", created.Name)
	}

	if created.Properties == nil || created.Properties.DiskSizeGB == nil ||
		*created.Properties.DiskSizeGB != 128 {
		t.Errorf("diskSizeGB=%v want 128", created.Properties.DiskSizeGB)
	}

	got, err := client.Get(ctx, "rg-1", "data-disk-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "data-disk-1" {
		t.Errorf("got.Name=%v", got.Name)
	}

	pager := client.NewListByResourceGroupPager("rg-1", nil)

	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, d := range page.Value {
			if d.Name != nil && *d.Name == "data-disk-1" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List did not return data-disk-1")
	}

	delPoller, err := client.BeginDelete(ctx, "rg-1", "data-disk-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("delete poll: %v", err)
	}
}
