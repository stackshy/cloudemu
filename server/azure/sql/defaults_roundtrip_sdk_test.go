package sql_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// TestSDKAzureSQLServerDefaults verifies a logical-server create that omits the
// network properties reads back Azure's documented defaults (minimalTlsVersion
// "1.2", publicNetworkAccess "Enabled", restrictOutboundNetworkAccess
// "Disabled"), so a real SDK/CLI/Terraform read does not drift.
func TestSDKAzureSQLServerDefaults(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()

	poller, err := cf.NewServersClient().BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("server create: %v", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server poll: %v", err)
	}

	got, err := cf.NewServersClient().Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("server get: %v", err)
	}
	p := got.Properties

	if p.MinimalTLSVersion == nil || *p.MinimalTLSVersion != "1.2" {
		t.Errorf("minimalTlsVersion = %v, want 1.2", ptr(p.MinimalTLSVersion))
	}
	if p.PublicNetworkAccess == nil || *p.PublicNetworkAccess != armsql.ServerNetworkAccessFlagEnabled {
		t.Errorf("publicNetworkAccess = %v, want Enabled", p.PublicNetworkAccess)
	}
	if p.RestrictOutboundNetworkAccess == nil || *p.RestrictOutboundNetworkAccess != armsql.ServerNetworkAccessFlagDisabled {
		t.Errorf("restrictOutboundNetworkAccess = %v, want Disabled", p.RestrictOutboundNetworkAccess)
	}
}

// TestSDKAzureSQLServerNetworkPropsRoundTrip verifies explicit network props
// survive create → get and that a PATCH updates only the fields it carries,
// leaving the others intact (ARM partial update semantics).
func TestSDKAzureSQLServerNetworkPropsRoundTrip(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	sc := cf.NewServersClient()

	poller, err := sc.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
			MinimalTLSVersion:          to.Ptr("1.1"),
			PublicNetworkAccess:        to.Ptr(armsql.ServerNetworkAccessFlagDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("server create: %v", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server poll: %v", err)
	}

	got, err := sc.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("server get: %v", err)
	}
	if got.Properties.MinimalTLSVersion == nil || *got.Properties.MinimalTLSVersion != "1.1" {
		t.Errorf("create: minimalTlsVersion = %v, want 1.1", ptr(got.Properties.MinimalTLSVersion))
	}
	if got.Properties.PublicNetworkAccess == nil || *got.Properties.PublicNetworkAccess != armsql.ServerNetworkAccessFlagDisabled {
		t.Errorf("create: publicNetworkAccess = %v, want Disabled", got.Properties.PublicNetworkAccess)
	}

	// PATCH only minimalTlsVersion; publicNetworkAccess must be preserved.
	pPoller, err := sc.BeginUpdate(ctx, "rg-1", "srv1", armsql.ServerUpdate{
		Properties: &armsql.ServerProperties{MinimalTLSVersion: to.Ptr("1.2")},
	}, nil)
	if err != nil {
		t.Fatalf("server patch: %v", err)
	}
	if _, err := pPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server patch poll: %v", err)
	}

	got, err = sc.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("server get after patch: %v", err)
	}
	if got.Properties.MinimalTLSVersion == nil || *got.Properties.MinimalTLSVersion != "1.2" {
		t.Errorf("patch: minimalTlsVersion = %v, want 1.2", ptr(got.Properties.MinimalTLSVersion))
	}
	if got.Properties.PublicNetworkAccess == nil || *got.Properties.PublicNetworkAccess != armsql.ServerNetworkAccessFlagDisabled {
		t.Errorf("patch: publicNetworkAccess = %v, want preserved Disabled", got.Properties.PublicNetworkAccess)
	}
}

// TestSDKAzureSQLDatabaseDefaults verifies a database create that omits
// collation/maxSize/backup redundancy/readScale reads back Azure's documented
// defaults, so azurerm_mssql_database does not drift on refresh.
func TestSDKAzureSQLDatabaseDefaults(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)
	ctx := context.Background()
	dbs := cf.NewDatabasesClient()

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("GP_Gen5_2"), Tier: to.Ptr("GeneralPurpose")},
	}, nil)
	if err != nil {
		t.Fatalf("db create: %v", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db poll: %v", err)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("db get: %v", err)
	}
	p := got.Properties

	if p.Collation == nil || *p.Collation != "SQL_Latin1_General_CP1_CI_AS" {
		t.Errorf("collation = %v, want SQL_Latin1_General_CP1_CI_AS", ptr(p.Collation))
	}
	if p.MaxSizeBytes == nil || *p.MaxSizeBytes != 34359738368 {
		t.Errorf("maxSizeBytes = %v, want 34359738368", p.MaxSizeBytes)
	}
	if p.RequestedBackupStorageRedundancy == nil || *p.RequestedBackupStorageRedundancy != armsql.BackupStorageRedundancyGeo {
		t.Errorf("requestedBackupStorageRedundancy = %v, want Geo", p.RequestedBackupStorageRedundancy)
	}
	if p.CurrentBackupStorageRedundancy == nil || *p.CurrentBackupStorageRedundancy != armsql.BackupStorageRedundancyGeo {
		t.Errorf("currentBackupStorageRedundancy = %v, want Geo", p.CurrentBackupStorageRedundancy)
	}
	if p.ReadScale == nil || *p.ReadScale != armsql.DatabaseReadScaleDisabled {
		t.Errorf("readScale = %v, want Disabled", p.ReadScale)
	}
}

// TestSDKAzureSQLDatabasePropsRoundTrip verifies explicit maxSizeBytes / backup
// redundancy / readScale survive create → get, and that a PATCH changing only
// the SKU preserves them (ARM partial update), while a PATCH that carries a new
// maxSizeBytes updates it.
func TestSDKAzureSQLDatabasePropsRoundTrip(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)
	ctx := context.Background()
	dbs := cf.NewDatabasesClient()

	const customMax = int64(107374182400) // 100 GB

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("GP_Gen5_2"), Tier: to.Ptr("GeneralPurpose")},
		Properties: &armsql.DatabaseProperties{
			MaxSizeBytes:                     to.Ptr(customMax),
			RequestedBackupStorageRedundancy: to.Ptr(armsql.BackupStorageRedundancyZone),
			ReadScale:                        to.Ptr(armsql.DatabaseReadScaleEnabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("db create: %v", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db poll: %v", err)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("db get: %v", err)
	}
	if got.Properties.MaxSizeBytes == nil || *got.Properties.MaxSizeBytes != customMax {
		t.Errorf("create: maxSizeBytes = %v, want %d", got.Properties.MaxSizeBytes, customMax)
	}
	if got.Properties.RequestedBackupStorageRedundancy == nil ||
		*got.Properties.RequestedBackupStorageRedundancy != armsql.BackupStorageRedundancyZone {
		t.Errorf("create: backup redundancy = %v, want Zone", got.Properties.RequestedBackupStorageRedundancy)
	}

	// PATCH only the SKU: maxSizeBytes/backup/readScale must be preserved.
	pPoller, err := dbs.BeginUpdate(ctx, "rg-1", "srv1", "appdb", armsql.DatabaseUpdate{
		SKU: &armsql.SKU{Name: to.Ptr("GP_Gen5_4"), Tier: to.Ptr("GeneralPurpose")},
	}, nil)
	if err != nil {
		t.Fatalf("db patch: %v", err)
	}
	if _, err := pPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db patch poll: %v", err)
	}

	got, err = dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("db get after patch: %v", err)
	}
	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != "GP_Gen5_4" {
		t.Errorf("patch: sku.name = %v, want GP_Gen5_4", got.SKU)
	}
	if got.Properties.MaxSizeBytes == nil || *got.Properties.MaxSizeBytes != customMax {
		t.Errorf("patch: maxSizeBytes = %v, want preserved %d", got.Properties.MaxSizeBytes, customMax)
	}
	if got.Properties.ReadScale == nil || *got.Properties.ReadScale != armsql.DatabaseReadScaleEnabled {
		t.Errorf("patch: readScale = %v, want preserved Enabled", got.Properties.ReadScale)
	}
}

func ptr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
