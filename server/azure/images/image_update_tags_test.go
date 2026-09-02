package images_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newImagesClient stands up a server with a source VM and a tagged image, then
// returns the images client for exercising the PATCH tag update.
func newImagesClient(t *testing.T, ctx context.Context) *armcompute.ImagesClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Images:          cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	opts := clientOpts(ts)

	vmClient, err := armcompute.NewVirtualMachinesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	vmPoller, err := vmClient.BeginCreateOrUpdate(ctx, "rg-1", "tag-src-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
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

	imgClient, err := armcompute.NewImagesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	imgPoller, err := imgClient.BeginCreateOrUpdate(ctx, "rg-1", "img-tags",
		armcompute.Image{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("dev"), "owner": to.Ptr("alice")},
			Properties: &armcompute.ImageProperties{
				SourceVirtualMachine: &armcompute.SubResource{
					ID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/tag-src-vm"),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("img create: %v", err)
	}

	if _, err := imgPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("img poll: %v", err)
	}

	return imgClient
}

// TestSDKImageUpdateTagsMerges verifies the PATCH tag update merges the supplied
// tags into the existing set (preserving untouched ones), overrides a key when
// re-supplied, and returns the full image resource.
func TestSDKImageUpdateTagsMerges(t *testing.T) {
	ctx := context.Background()
	imgClient := newImagesClient(t, ctx)

	poller, err := imgClient.BeginUpdate(ctx, "rg-1", "img-tags",
		armcompute.ImageUpdate{Tags: map[string]*string{"team": to.Ptr("data"), "env": to.Ptr("prod")}}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	updated, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("update poll: %v", err)
	}

	if updated.Name == nil || *updated.Name != "img-tags" {
		t.Errorf("name=%v want img-tags", updated.Name)
	}

	// Untouched osDisk / provisioning state survive the tag PATCH.
	if updated.Properties == nil || updated.Properties.ProvisioningState == nil ||
		*updated.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState not preserved: %+v", updated.Properties)
	}

	wantTags := map[string]string{"env": "prod", "owner": "alice", "team": "data"}
	assertTags(t, updated.Tags, wantTags)

	// A fresh GET reports the same merged tags.
	got, err := imgClient.Get(ctx, "rg-1", "img-tags", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertTags(t, got.Tags, wantTags)
}

// TestSDKImageUpdateTagsMissing verifies a PATCH on a missing image is a 404.
func TestSDKImageUpdateTagsMissing(t *testing.T) {
	ctx := context.Background()
	imgClient := newImagesClient(t, ctx)

	_, err := imgClient.BeginUpdate(ctx, "rg-1", "no-such-image",
		armcompute.ImageUpdate{Tags: map[string]*string{"x": to.Ptr("y")}}, nil)
	if err == nil {
		t.Fatal("expected error updating a missing image, got nil")
	}
}

func assertTags(t *testing.T, got map[string]*string, want map[string]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("tag count=%d want %d (%v)", len(got), len(want), got)
	}

	for k, v := range want {
		if got[k] == nil || *got[k] != v {
			t.Errorf("tag %q=%v want %q", k, got[k], v)
		}
	}
}
