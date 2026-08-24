package postgresflex_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

// TestSDKPostgresFlexZoneAndLocationRoundTrip asserts that properties.availabilityZone
// (the compute zone id "1"/"2"/"3") round-trips separately from the top-level
// location (the Azure region), rather than the zone being overwritten with the
// region string.
func TestSDKPostgresFlexZoneAndLocationRoundTrip(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	poller, err := servers.BeginCreate(ctx, "rg-1", "zsrv", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		Properties: &armpostgresqlflexibleservers.ServerProperties{
			AvailabilityZone: to.Ptr("2"),
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
	if az == nil || *az != "2" {
		t.Fatalf("availabilityZone = %v, want 2", az)
	}
}

// TestSDKPostgresFlexRePutAppliesUpdate covers the idempotent-PUT upsert: a
// second BeginCreate with changed storage/SKU must apply the change rather than
// returning the stale record.
func TestSDKPostgresFlexRePutAppliesUpdate(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	first, err := servers.BeginCreate(ctx, "rg-1", "reput", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_B1ms")},
		Properties: &armpostgresqlflexibleservers.ServerProperties{
			Storage: &armpostgresqlflexibleservers.Storage{StorageSizeGB: to.Ptr(int32(64))},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := first.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("first PollUntilDone: %v", err)
	}

	second, err := servers.BeginCreate(ctx, "rg-1", "reput", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_D2s_v3")},
		Properties: &armpostgresqlflexibleservers.ServerProperties{
			Storage: &armpostgresqlflexibleservers.Storage{StorageSizeGB: to.Ptr(int32(128))},
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

	if got.Server.SKU == nil || *got.Server.SKU.Name != "Standard_D2s_v3" {
		t.Fatalf("SKU after re-PUT = %+v, want Standard_D2s_v3", got.Server.SKU)
	}

	if got.Server.Properties == nil || got.Server.Properties.Storage == nil ||
		got.Server.Properties.Storage.StorageSizeGB == nil ||
		*got.Server.Properties.Storage.StorageSizeGB != 128 {
		t.Fatalf("storage after re-PUT != 128")
	}
}
