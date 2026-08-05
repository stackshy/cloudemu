package azuresql_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// TestSDKAzureSQLManagedInstanceBackupRedundancy verifies the backup storage
// redundancy the armsql SDK sends as requestedBackupStorageRedundancy survives
// a managed-instance create → get round-trip and is echoed back in the read
// form (currentBackupStorageRedundancy) the SDK deserializes.
func TestSDKAzureSQLManagedInstanceBackupRedundancy(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	mic := cf.NewManagedInstancesClient()

	poller, err := mic.BeginCreateOrUpdate(ctx, "rg-1", "mi1", armsql.ManagedInstance{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ManagedInstanceProperties{
			AdministratorLogin:               to.Ptr("miadmin"),
			VCores:                           to.Ptr(int32(4)),
			SubnetID:                         to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vn/subnets/mi"),
			RequestedBackupStorageRedundancy: to.Ptr(armsql.BackupStorageRedundancyZone),
		},
	}, nil)
	if err != nil {
		t.Fatalf("MI create: %v", err)
	}

	createResp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("MI create poll: %v", err)
	}

	if got := createResp.Properties.CurrentBackupStorageRedundancy; got == nil || *got != armsql.BackupStorageRedundancyZone {
		t.Fatalf("create: currentBackupStorageRedundancy = %v, want Zone", got)
	}

	got, err := mic.Get(ctx, "rg-1", "mi1", nil)
	if err != nil {
		t.Fatalf("MI Get: %v", err)
	}

	if v := got.Properties.CurrentBackupStorageRedundancy; v == nil || *v != armsql.BackupStorageRedundancyZone {
		t.Fatalf("get: currentBackupStorageRedundancy = %v, want Zone", v)
	}

	if v := got.Properties.RequestedBackupStorageRedundancy; v == nil || *v != armsql.BackupStorageRedundancyZone {
		t.Fatalf("get: requestedBackupStorageRedundancy = %v, want Zone", v)
	}
}

// TestSDKAzureSQLDatabaseCostFields verifies a database's SKU (name + tier) and
// the zoneRedundant HA flag survive create → get through the armsql SDK, backed
// by the Databases capability so the same record is discoverable in Resource
// Graph.
func TestSDKAzureSQLDatabaseCostFields(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	dbs := cf.NewDatabasesClient()

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("GP_Gen5_4"), Tier: to.Ptr("GeneralPurpose")},
		Properties: &armsql.DatabaseProperties{
			ZoneRedundant: to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("DB create: %v", err)
	}

	createResp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("DB create poll: %v", err)
	}

	if createResp.SKU == nil || createResp.SKU.Name == nil || *createResp.SKU.Name != "GP_Gen5_4" {
		t.Fatalf("create: sku.name = %v, want GP_Gen5_4", createResp.SKU)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("DB Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != "GP_Gen5_4" {
		t.Fatalf("get: sku.name = %v, want GP_Gen5_4", got.SKU)
	}

	if got.SKU.Tier == nil || *got.SKU.Tier != "GeneralPurpose" {
		t.Fatalf("get: sku.tier = %v, want GeneralPurpose", got.SKU.Tier)
	}

	if got.Properties == nil || got.Properties.CurrentSKU == nil ||
		got.Properties.CurrentSKU.Name == nil || *got.Properties.CurrentSKU.Name != "GP_Gen5_4" {
		t.Fatalf("get: properties.currentSku.name = %v, want GP_Gen5_4", got.Properties)
	}

	if got.Properties.ZoneRedundant == nil || !*got.Properties.ZoneRedundant {
		t.Fatalf("get: properties.zoneRedundant = %v, want true", got.Properties.ZoneRedundant)
	}
}
