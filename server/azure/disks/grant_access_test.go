package disks_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKDiskGrantAndRevokeAccess drives DisksClient.BeginGrantAccess /
// BeginRevokeAccess (the beginGetAccess / endGetAccess actions) through a real
// armcompute client and asserts a time-bounded SAS URI is returned and can be
// revoked.
func TestSDKDiskGrantAndRevokeAccess(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newDisksClient(t, ts)
	ctx := context.Background()

	createPoller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "sas-disk",
		armcompute.Disk{
			Location: to.Ptr("eastus"),
			SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
			Properties: &armcompute.DiskProperties{
				CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
				DiskSizeGB:   to.Ptr[int32](64),
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	grantPoller, err := client.BeginGrantAccess(ctx, "rg-1", "sas-disk",
		armcompute.GrantAccessData{
			Access:            to.Ptr(armcompute.AccessLevelRead),
			DurationInSeconds: to.Ptr[int32](300),
		}, nil)
	if err != nil {
		t.Fatalf("BeginGrantAccess: %v", err)
	}

	granted, err := grantPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("grant poll: %v", err)
	}

	if granted.AccessSAS == nil || *granted.AccessSAS == "" {
		t.Fatal("BeginGrantAccess returned empty accessSAS")
	}

	sas := *granted.AccessSAS
	if !strings.HasPrefix(sas, "https://") || !strings.Contains(sas, "sig=") || !strings.Contains(sas, "se=") {
		t.Errorf("accessSAS is not a time-bounded SAS URI: %s", sas)
	}

	revokePoller, err := client.BeginRevokeAccess(ctx, "rg-1", "sas-disk", nil)
	if err != nil {
		t.Fatalf("BeginRevokeAccess: %v", err)
	}

	if _, err := revokePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("revoke poll: %v", err)
	}
}

// TestSDKDiskGrantAccessMissingDisk asserts beginGetAccess on a disk that does
// not exist fails rather than returning a bogus SAS.
func TestSDKDiskGrantAccessMissingDisk(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloudP.VirtualMachines,
		Disks:           cloudP.VirtualMachines,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newDisksClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginGrantAccess(ctx, "rg-1", "no-such-disk",
		armcompute.GrantAccessData{
			Access:            to.Ptr(armcompute.AccessLevelRead),
			DurationInSeconds: to.Ptr[int32](300),
		}, nil)
	if err == nil {
		if _, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err == nil {
			t.Fatal("expected BeginGrantAccess on a missing disk to fail")
		}
	}
}
