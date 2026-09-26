package disks_test

import (
	"context"
	"errors"
	"net/http"
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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

var fastPoll = &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}

func newUpdateTestServer(t *testing.T) (*httptest.Server, *armcompute.DisksClient) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines:   cloudP.VirtualMachines,
		Disks:             cloudP.VirtualMachines,
		ResourceDiscovery: cloudP.ResourceDiscovery,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)
	ensureRG(t, ts, "sub-1", "rg-1")

	return ts, newDisksClient(t, ts)
}

func createDiskForUpdate(t *testing.T, client *armcompute.DisksClient, name string, sku armcompute.DiskStorageAccountTypes) {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", name, armcompute.Disk{
		Location: to.Ptr("eastus"),
		SKU:      &armcompute.DiskSKU{Name: to.Ptr(sku)},
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		Properties: &armcompute.DiskProperties{
			CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
			DiskSizeGB:   to.Ptr[int32](64),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, fastPoll); err != nil {
		t.Fatalf("create poll: %v", err)
	}
}

func beginUpdate(t *testing.T, client *armcompute.DisksClient, name string, upd armcompute.DiskUpdate) (armcompute.Disk, error) {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginUpdate(ctx, "rg-1", name, upd, nil)
	if err != nil {
		return armcompute.Disk{}, err
	}

	resp, err := poller.PollUntilDone(ctx, fastPoll)
	if err != nil {
		t.Fatalf("update poll: %v", err)
	}

	return resp.Disk, nil
}

// TestSDKDiskUpdate drives armcompute DisksClient.BeginUpdate + PollUntilDone
// end to end: size grows, SKU switches to PremiumV2_LRS with provisioned
// performance, tags are replaced, and identity (id/uniqueId/timeCreated) is kept.
func TestSDKDiskUpdate(t *testing.T) {
	_, client := newUpdateTestServer(t)
	ctx := context.Background()

	createDiskForUpdate(t, client, "upd-disk", armcompute.DiskStorageAccountTypesPremiumLRS)

	before, err := client.Get(ctx, "rg-1", "upd-disk", nil)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	updated, err := beginUpdate(t, client, "upd-disk", armcompute.DiskUpdate{
		SKU:  &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumV2LRS)},
		Tags: map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("storage")},
		Properties: &armcompute.DiskUpdateProperties{
			DiskSizeGB:        to.Ptr[int32](128),
			DiskIOPSReadWrite: to.Ptr[int64](4000),
			DiskMBpsReadWrite: to.Ptr[int64](250),
		},
	})
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	assertUpdatedDisk(t, "poller result", updated)

	got, err := client.Get(ctx, "rg-1", "upd-disk", nil)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}

	assertUpdatedDisk(t, "get", got.Disk)

	if *got.ID != *before.ID || *got.Properties.UniqueID != *before.Properties.UniqueID {
		t.Errorf("identity changed: id %s -> %s, uniqueId %s -> %s",
			*before.ID, *got.ID, *before.Properties.UniqueID, *got.Properties.UniqueID)
	}

	if !got.Properties.TimeCreated.Equal(*before.Properties.TimeCreated) {
		t.Errorf("timeCreated changed: %v -> %v", before.Properties.TimeCreated, got.Properties.TimeCreated)
	}
}

func assertUpdatedDisk(t *testing.T, stage string, d armcompute.Disk) {
	t.Helper()

	p := d.Properties
	if p == nil {
		t.Fatalf("%s: properties nil", stage)
	}

	if p.DiskSizeGB == nil || *p.DiskSizeGB != 128 {
		t.Errorf("%s: diskSizeGB=%v want 128", stage, p.DiskSizeGB)
	}

	if p.DiskIOPSReadWrite == nil || *p.DiskIOPSReadWrite != 4000 {
		t.Errorf("%s: diskIOPSReadWrite=%v want 4000", stage, p.DiskIOPSReadWrite)
	}

	if p.DiskMBpsReadWrite == nil || *p.DiskMBpsReadWrite != 250 {
		t.Errorf("%s: diskMBpsReadWrite=%v want 250", stage, p.DiskMBpsReadWrite)
	}

	if d.SKU == nil || d.SKU.Name == nil || *d.SKU.Name != armcompute.DiskStorageAccountTypesPremiumV2LRS {
		t.Errorf("%s: sku=%v want PremiumV2_LRS", stage, d.SKU)
	}

	if len(d.Tags) != 2 || d.Tags["env"] == nil || *d.Tags["env"] != "prod" || d.Tags["team"] == nil {
		t.Errorf("%s: tags=%v want {env:prod, team:storage}", stage, d.Tags)
	}
}

// TestSDKDiskUpdateOmittedFieldsKept checks that a tags-only PATCH leaves size
// and SKU alone, and that an empty-body PATCH is a no-op.
func TestSDKDiskUpdateOmittedFieldsKept(t *testing.T) {
	_, client := newUpdateTestServer(t)

	createDiskForUpdate(t, client, "keep-disk", armcompute.DiskStorageAccountTypesStandardSSDLRS)

	got, err := beginUpdate(t, client, "keep-disk", armcompute.DiskUpdate{
		Tags: map[string]*string{"owner": to.Ptr("alice")},
	})
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if *got.Properties.DiskSizeGB != 64 {
		t.Errorf("diskSizeGB=%d want 64 (unchanged)", *got.Properties.DiskSizeGB)
	}

	if *got.SKU.Name != armcompute.DiskStorageAccountTypesStandardSSDLRS {
		t.Errorf("sku=%s want StandardSSD_LRS (unchanged)", *got.SKU.Name)
	}

	if len(got.Tags) != 1 || *got.Tags["owner"] != "alice" {
		t.Errorf("tags=%v want exactly {owner:alice}", got.Tags)
	}

	got, err = beginUpdate(t, client, "keep-disk", armcompute.DiskUpdate{})
	if err != nil {
		t.Fatalf("empty BeginUpdate: %v", err)
	}

	if *got.Properties.DiskSizeGB != 64 || len(got.Tags) != 1 {
		t.Errorf("empty PATCH changed the disk: size=%d tags=%v", *got.Properties.DiskSizeGB, got.Tags)
	}
}

// TestSDKDiskUpdateRejections covers the 400s real Azure returns for a shrink
// and for provisioned performance on a SKU that does not support it, plus the
// 404 for a missing disk. A rejected PATCH must leave the disk untouched.
func TestSDKDiskUpdateRejections(t *testing.T) {
	_, client := newUpdateTestServer(t)
	ctx := context.Background()

	createDiskForUpdate(t, client, "rej-disk", armcompute.DiskStorageAccountTypesStandardLRS)

	cases := []struct {
		name     string
		disk     string
		upd      armcompute.DiskUpdate
		status   int
		wantCode string
	}{
		{"shrink", "rej-disk", armcompute.DiskUpdate{
			Properties: &armcompute.DiskUpdateProperties{DiskSizeGB: to.Ptr[int32](32)},
		}, http.StatusBadRequest, "BadRequest"},
		{"iops on Standard_LRS", "rej-disk", armcompute.DiskUpdate{
			Properties: &armcompute.DiskUpdateProperties{DiskIOPSReadWrite: to.Ptr[int64](3000)},
		}, http.StatusBadRequest, "InvalidParameter"},
		{"missing disk", "no-such-disk", armcompute.DiskUpdate{
			Tags: map[string]*string{"a": to.Ptr("b")},
		}, http.StatusNotFound, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := beginUpdate(t, client, tc.disk, tc.upd)

			var respErr *azcore.ResponseError
			if !errors.As(err, &respErr) {
				t.Fatalf("err=%v, want *azcore.ResponseError", err)
			}

			if respErr.StatusCode != tc.status {
				t.Errorf("status=%d want %d", respErr.StatusCode, tc.status)
			}

			if tc.wantCode != "" && respErr.ErrorCode != tc.wantCode {
				t.Errorf("code=%q want %q", respErr.ErrorCode, tc.wantCode)
			}
		})
	}

	got, err := client.Get(ctx, "rg-1", "rej-disk", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.Properties.DiskSizeGB != 64 || got.Properties.DiskIOPSReadWrite != nil {
		t.Errorf("rejected PATCH mutated disk: size=%d iops=%v", *got.Properties.DiskSizeGB, got.Properties.DiskIOPSReadWrite)
	}
}

// TestSDKDiskUpdateSKUDowngradeDropsPerf checks that moving a PremiumV2_LRS
// disk to a SKU without settable performance drops the provisioned IOPS/MBps.
func TestSDKDiskUpdateSKUDowngradeDropsPerf(t *testing.T) {
	_, client := newUpdateTestServer(t)

	createDiskForUpdate(t, client, "perf-disk", armcompute.DiskStorageAccountTypesPremiumV2LRS)

	if _, err := beginUpdate(t, client, "perf-disk", armcompute.DiskUpdate{
		Properties: &armcompute.DiskUpdateProperties{DiskIOPSReadWrite: to.Ptr[int64](5000)},
	}); err != nil {
		t.Fatalf("set iops: %v", err)
	}

	got, err := beginUpdate(t, client, "perf-disk", armcompute.DiskUpdate{
		SKU: &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesPremiumLRS)},
	})
	if err != nil {
		t.Fatalf("downgrade: %v", err)
	}

	if got.Properties.DiskIOPSReadWrite != nil {
		t.Errorf("diskIOPSReadWrite=%d after moving to Premium_LRS, want omitted", *got.Properties.DiskIOPSReadWrite)
	}
}

// TestSDKDiskUpdateVisibleInResourceGraph checks that a PATCHed size and tag
// set is what Resource Graph reports for the disk afterwards.
func TestSDKDiskUpdateVisibleInResourceGraph(t *testing.T) {
	ts, client := newUpdateTestServer(t)

	createDiskForUpdate(t, client, "arg-disk", armcompute.DiskStorageAccountTypesPremiumLRS)

	if _, err := beginUpdate(t, client, "arg-disk", armcompute.DiskUpdate{
		Tags:       map[string]*string{"env": to.Ptr("prod")},
		Properties: &armcompute.DiskUpdateProperties{DiskSizeGB: to.Ptr[int32](256)},
	}); err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	row := queryDiskRow(t, ts)

	props, _ := row["properties"].(map[string]any)
	if size, _ := props["diskSizeGB"].(float64); size != 256 {
		t.Errorf("resource graph diskSizeGB=%v want 256", props["diskSizeGB"])
	}

	tags, _ := row["tags"].(map[string]any)
	if tags["env"] != "prod" {
		t.Errorf("resource graph tags=%v want env=prod", tags)
	}
}

func queryDiskRow(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
			},
		},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armresourcegraph.NewClientFactory(fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	out, err := cf.NewClient().Resources(context.Background(), armresourcegraph.QueryRequest{
		Query: to.Ptr("Resources | where type =~ 'microsoft.compute/disks'"),
	}, nil)
	if err != nil {
		t.Fatalf("Resources: %v", err)
	}

	data, _ := out.Data.([]any)
	if len(data) != 1 {
		t.Fatalf("resource graph rows=%d want 1", len(data))
	}

	row, _ := data[0].(map[string]any)

	return row
}
