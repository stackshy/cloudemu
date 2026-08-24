package mysqlflex_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

// TestSDKMySQLFlexZoneAndLocationRoundTrip asserts that properties.availabilityZone
// (the compute zone id "1"/"2"/"3") round-trips separately from the top-level
// location (the Azure region).
func TestSDKMySQLFlexZoneAndLocationRoundTrip(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	poller, err := servers.BeginCreate(ctx, "rg-1", "zsrv", armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		Properties: &armmysqlflexibleservers.ServerProperties{
			AvailabilityZone: to.Ptr("3"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "zsrv", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Server.Location == nil || *got.Server.Location != "eastus" {
		t.Fatalf("location = %v, want eastus", got.Server.Location)
	}

	az := got.Server.Properties.AvailabilityZone
	if az == nil || *az != "3" {
		t.Fatalf("availabilityZone = %v, want 3", az)
	}
}

// TestSDKMySQLFlexRePutAppliesUpdate covers the idempotent-PUT upsert: a second
// BeginCreate with changed storage/SKU must apply the change rather than
// returning the stale record.
func TestSDKMySQLFlexRePutAppliesUpdate(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	first, err := servers.BeginCreate(ctx, "rg-1", "reput", armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armmysqlflexibleservers.SKU{Name: to.Ptr("Standard_B1ms")},
		Properties: &armmysqlflexibleservers.ServerProperties{
			Storage: &armmysqlflexibleservers.Storage{StorageSizeGB: to.Ptr(int32(64))},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := first.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("first PollUntilDone: %v", err)
	}

	second, err := servers.BeginCreate(ctx, "rg-1", "reput", armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armmysqlflexibleservers.SKU{Name: to.Ptr("Standard_D2ds_v4")},
		Properties: &armmysqlflexibleservers.ServerProperties{
			Storage: &armmysqlflexibleservers.Storage{StorageSizeGB: to.Ptr(int32(256))},
		},
	}, nil)
	if err != nil {
		t.Fatalf("re-PUT BeginCreate: %v", err)
	}

	if _, err := second.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("re-PUT PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "reput", nil)
	if err != nil {
		t.Fatalf("Get after re-PUT: %v", err)
	}

	if got.Server.SKU == nil || *got.Server.SKU.Name != "Standard_D2ds_v4" {
		t.Fatalf("SKU after re-PUT = %+v, want Standard_D2ds_v4", got.Server.SKU)
	}

	if got.Server.Properties == nil || got.Server.Properties.Storage == nil ||
		got.Server.Properties.Storage.StorageSizeGB == nil ||
		*got.Server.Properties.Storage.StorageSizeGB != 256 {
		t.Fatalf("storage after re-PUT != 256")
	}
}
