package snapshots_test

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

func newSnapshotsClient(t *testing.T, ts *httptest.Server) *armcompute.SnapshotsClient {
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

	client, err := armcompute.NewSnapshotsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func TestSDKSnapshotRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
		Snapshots:       cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	// Create a source disk via the disks SDK client first — snapshots need a
	// real source.
	disksClient, err := armcompute.NewDisksClient("sub-1", fakeCred{}, &arm.ClientOptions{
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
	})
	if err != nil {
		t.Fatal(err)
	}

	diskPoller, err := disksClient.BeginCreateOrUpdate(ctx, "rg-1", "src-disk",
		armcompute.Disk{
			Location: to.Ptr("eastus"),
			SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
			Properties: &armcompute.DiskProperties{
				CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
				DiskSizeGB:   to.Ptr[int32](64),
			},
		}, nil)
	if err != nil {
		t.Fatalf("disk BeginCreateOrUpdate: %v", err)
	}

	if _, err := diskPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("disk poll: %v", err)
	}

	client := newSnapshotsClient(t, ts)

	createPoller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "snap-1",
		armcompute.Snapshot{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.SnapshotProperties{
				CreationData: &armcompute.CreationData{
					CreateOption:     to.Ptr(armcompute.DiskCreateOptionCopy),
					SourceResourceID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks/src-disk"),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := createPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if created.Name == nil || *created.Name != "snap-1" {
		t.Errorf("name=%v want snap-1", created.Name)
	}

	got, err := client.Get(ctx, "rg-1", "snap-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "snap-1" {
		t.Errorf("got.Name=%v", got.Name)
	}

	pager := client.NewListByResourceGroupPager("rg-1", nil)

	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, s := range page.Value {
			if s.Name != nil && *s.Name == "snap-1" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List did not return snap-1")
	}

	delPoller, err := client.BeginDelete(ctx, "rg-1", "snap-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("delete poll: %v", err)
	}
}
