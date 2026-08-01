package azuresql_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

func mustCreateSQLServer(t *testing.T, cf *armsql.ClientFactory) {
	t.Helper()

	ctx := context.Background()

	poller, err := cf.NewServersClient().BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
			Version:                    to.Ptr("12.0"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("server BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("server PollUntilDone: %v", err)
	}
}

func TestSDKAzureSQLFirewallRules(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	fw := cf.NewFirewallRulesClient()

	if _, err := fw.CreateOrUpdate(ctx, "rg-1", "srv1", "office", armsql.FirewallRule{
		Properties: &armsql.ServerFirewallRuleProperties{
			StartIPAddress: to.Ptr("10.0.0.1"),
			EndIPAddress:   to.Ptr("10.0.0.255"),
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	got, err := fw.Get(ctx, "rg-1", "srv1", "office", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.StartIPAddress == nil || *got.Properties.StartIPAddress != "10.0.0.1" {
		t.Fatalf("start ip: got %v", got.Properties)
	}

	page, err := fw.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d firewall rules, want 1", len(page.Value))
	}

	if _, err := fw.Delete(ctx, "rg-1", "srv1", "office", nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := fw.Get(ctx, "rg-1", "srv1", "office", nil); err == nil {
		t.Fatal("expected NotFound after firewall rule delete")
	}
}

func TestSDKAzureSQLVNetRules(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	vr := cf.NewVirtualNetworkRulesClient()

	poller, err := vr.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "vnet1", armsql.VirtualNetworkRule{
		Properties: &armsql.VirtualNetworkRuleProperties{
			VirtualNetworkSubnetID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vn/subnets/s1"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("vnet PollUntilDone: %v", err)
	}

	got, err := vr.Get(ctx, "rg-1", "srv1", "vnet1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.VirtualNetworkSubnetID == nil {
		t.Fatal("expected subnet id set")
	}

	page, err := vr.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d vnet rules, want 1", len(page.Value))
	}

	delPoller, err := vr.BeginDelete(ctx, "rg-1", "srv1", "vnet1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("vnet delete PollUntilDone: %v", err)
	}
}

func TestSDKAzureSQLElasticPools(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	ep := cf.NewElasticPoolsClient()

	poller, err := ep.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "pool1", armsql.ElasticPool{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("StandardPool"), Tier: to.Ptr("Standard")},
		Properties: &armsql.ElasticPoolProperties{
			MaxSizeBytes:        to.Ptr(int64(107374182400)),
			PerDatabaseSettings: &armsql.ElasticPoolPerDatabaseSettings{MinCapacity: to.Ptr(0.0), MaxCapacity: to.Ptr(50.0)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool PollUntilDone: %v", err)
	}

	got, err := ep.Get(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != "StandardPool" {
		t.Fatalf("sku: got %v", got.SKU)
	}

	page, err := ep.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d pools, want 1", len(page.Value))
	}

	delPoller, err := ep.BeginDelete(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool delete PollUntilDone: %v", err)
	}
}

func TestSDKAzureSQLFailoverGroups(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	fg := cf.NewFailoverGroupsClient()

	poller, err := fg.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "fg1", armsql.FailoverGroup{
		Properties: &armsql.FailoverGroupProperties{
			ReadWriteEndpoint: &armsql.FailoverGroupReadWriteEndpoint{
				FailoverPolicy: to.Ptr(armsql.ReadWriteEndpointFailoverPolicyManual),
			},
			PartnerServers: []*armsql.PartnerInfo{{
				ID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Sql/servers/partnersrv"),
			}},
			Databases: []*string{to.Ptr("db1")},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("fg PollUntilDone: %v", err)
	}

	got, err := fg.Get(ctx, "rg-1", "srv1", "fg1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.ReplicationRole == nil ||
		*got.Properties.ReplicationRole != armsql.FailoverGroupReplicationRolePrimary {
		t.Fatalf("expected Primary role, got %v", got.Properties)
	}

	// Failover flips the local role to Secondary.
	foPoller, err := fg.BeginFailover(ctx, "rg-1", "srv1", "fg1", nil)
	if err != nil {
		t.Fatalf("BeginFailover: %v", err)
	}

	foResp, err := foPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("failover PollUntilDone: %v", err)
	}

	if foResp.Properties == nil || foResp.Properties.ReplicationRole == nil ||
		*foResp.Properties.ReplicationRole != armsql.FailoverGroupReplicationRoleSecondary {
		t.Fatalf("expected Secondary role after failover, got %v", foResp.Properties)
	}

	// The forced-failover verb (a distinct 4th path segment) routes to the same
	// action and flips the role back to Primary.
	forcePoller, err := fg.BeginForceFailoverAllowDataLoss(ctx, "rg-1", "srv1", "fg1", nil)
	if err != nil {
		t.Fatalf("BeginForceFailoverAllowDataLoss: %v", err)
	}

	forceResp, err := forcePoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("force failover PollUntilDone: %v", err)
	}

	if forceResp.Properties == nil || forceResp.Properties.ReplicationRole == nil ||
		*forceResp.Properties.ReplicationRole != armsql.FailoverGroupReplicationRolePrimary {
		t.Fatalf("expected Primary role after force failover, got %v", forceResp.Properties)
	}

	// PATCH (BeginUpdate) merges — changing the grace period keeps the partner.
	patchPoller, err := fg.BeginUpdate(ctx, "rg-1", "srv1", "fg1", armsql.FailoverGroupUpdate{
		Properties: &armsql.FailoverGroupUpdateProperties{
			ReadWriteEndpoint: &armsql.FailoverGroupReadWriteEndpoint{
				FailoverPolicy:                         to.Ptr(armsql.ReadWriteEndpointFailoverPolicyAutomatic),
				FailoverWithDataLossGracePeriodMinutes: to.Ptr(int32(120)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("fg BeginUpdate: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("fg patch PollUntilDone: %v", err)
	}

	// List the failover groups on the server.
	fgPage, err := fg.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("fg List: %v", err)
	}

	if len(fgPage.Value) != 1 {
		t.Fatalf("got %d failover groups, want 1", len(fgPage.Value))
	}

	delPoller, err := fg.BeginDelete(ctx, "rg-1", "srv1", "fg1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("fg delete PollUntilDone: %v", err)
	}
}

func TestSDKAzureSQLAADAdmin(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	aad := cf.NewServerAzureADAdministratorsClient()

	poller, err := aad.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.AdministratorNameActiveDirectory,
		armsql.ServerAzureADAdministrator{
			Properties: &armsql.AdministratorProperties{
				AdministratorType: to.Ptr(armsql.AdministratorTypeActiveDirectory),
				Login:             to.Ptr("dba-group"),
				Sid:               to.Ptr("00000000-0000-0000-0000-000000000001"),
				TenantID:          to.Ptr("00000000-0000-0000-0000-0000000000ff"),
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("aad PollUntilDone: %v", err)
	}

	got, err := aad.Get(ctx, "rg-1", "srv1", armsql.AdministratorNameActiveDirectory, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Login == nil || *got.Properties.Login != "dba-group" {
		t.Fatalf("login: got %v", got.Properties)
	}

	page, err := aad.NewListByServerPager("rg-1", "srv1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d admins, want 1", len(page.Value))
	}

	delPoller, err := aad.BeginDelete(ctx, "rg-1", "srv1", armsql.AdministratorNameActiveDirectory, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("aad delete PollUntilDone: %v", err)
	}
}

func TestSDKAzureSQLManagedInstances(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()

	mic := cf.NewManagedInstancesClient()

	poller, err := mic.BeginCreateOrUpdate(ctx, "rg-1", "mi1", armsql.ManagedInstance{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("GP_Gen5"), Tier: to.Ptr("GeneralPurpose")},
		Properties: &armsql.ManagedInstanceProperties{
			AdministratorLogin: to.Ptr("miadmin"),
			VCores:             to.Ptr(int32(4)),
			StorageSizeInGB:    to.Ptr(int32(32)),
			SubnetID:           to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vn/subnets/mi"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("MI BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MI PollUntilDone: %v", err)
	}

	got, err := mic.Get(ctx, "rg-1", "mi1", nil)
	if err != nil {
		t.Fatalf("MI Get: %v", err)
	}

	if got.Properties == nil || got.Properties.AdministratorLogin == nil || *got.Properties.AdministratorLogin != "miadmin" {
		t.Fatalf("MI admin login: got %v", got.Properties)
	}

	page, err := mic.NewListByResourceGroupPager("rg-1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("MI List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d managed instances, want 1", len(page.Value))
	}

	// Failover (the managed-instance lifecycle action this SDK version exposes).
	foPoller, err := mic.BeginFailover(ctx, "rg-1", "mi1", nil)
	if err != nil {
		t.Fatalf("MI BeginFailover: %v", err)
	}

	if _, err := foPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MI failover: %v", err)
	}

	// Managed database.
	mdc := cf.NewManagedDatabasesClient()

	dbPoller, err := mdc.BeginCreateOrUpdate(ctx, "rg-1", "mi1", "appdb", armsql.ManagedDatabase{
		Location:   to.Ptr("eastus"),
		Properties: &armsql.ManagedDatabaseProperties{Collation: to.Ptr("SQL_Latin1_General_CP1_CI_AS")},
	}, nil)
	if err != nil {
		t.Fatalf("MDB BeginCreateOrUpdate: %v", err)
	}

	if _, err := dbPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MDB PollUntilDone: %v", err)
	}

	gotDB, err := mdc.Get(ctx, "rg-1", "mi1", "appdb", nil)
	if err != nil {
		t.Fatalf("MDB Get: %v", err)
	}

	if gotDB.Name == nil || *gotDB.Name != "appdb" {
		t.Fatalf("MDB name: got %v", gotDB.Name)
	}

	dbPage, err := mdc.NewListByInstancePager("rg-1", "mi1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("MDB List: %v", err)
	}

	if len(dbPage.Value) != 1 {
		t.Fatalf("got %d managed databases, want 1", len(dbPage.Value))
	}

	// Explicitly delete the managed database (DeleteManagedDatabase handler).
	mdbDelPoller, err := mdc.BeginDelete(ctx, "rg-1", "mi1", "appdb", nil)
	if err != nil {
		t.Fatalf("MDB BeginDelete: %v", err)
	}

	if _, err := mdbDelPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MDB delete: %v", err)
	}

	if _, err := mdc.Get(ctx, "rg-1", "mi1", "appdb", nil); err == nil {
		t.Fatal("expected NotFound after managed database delete")
	}

	// Delete the instance; managed databases cascade.
	delPoller, err := mic.BeginDelete(ctx, "rg-1", "mi1", nil)
	if err != nil {
		t.Fatalf("MI BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MI delete: %v", err)
	}

	if _, err := mic.Get(ctx, "rg-1", "mi1", nil); err == nil {
		t.Fatal("expected NotFound after managed instance delete")
	}
}

func TestSDKAzureSQLElasticPoolPatchMerge(t *testing.T) {
	cf := newFactory(t)
	mustCreateSQLServer(t, cf)

	ctx := context.Background()
	ep := cf.NewElasticPoolsClient()

	poller, err := ep.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "pool1", armsql.ElasticPool{
		Location:   to.Ptr("eastus"),
		SKU:        &armsql.SKU{Name: to.Ptr("StandardPool"), Tier: to.Ptr("Standard")},
		Properties: &armsql.ElasticPoolProperties{MaxSizeBytes: to.Ptr(int64(107374182400))},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool create: %v", err)
	}

	// PATCH only maxSizeBytes — SKU must survive the merge.
	up, err := ep.BeginUpdate(ctx, "rg-1", "srv1", "pool1", armsql.ElasticPoolUpdate{
		Properties: &armsql.ElasticPoolUpdateProperties{MaxSizeBytes: to.Ptr(int64(214748364800))},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := up.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("pool patch: %v", err)
	}

	got, err := ep.Get(ctx, "rg-1", "srv1", "pool1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != "StandardPool" {
		t.Fatalf("PATCH wiped the SKU: %v", got.SKU)
	}

	if got.Properties == nil || got.Properties.MaxSizeBytes == nil || *got.Properties.MaxSizeBytes != 214748364800 {
		t.Fatalf("PATCH did not apply maxSizeBytes: %v", got.Properties)
	}
}

func TestSDKAzureSQLManagedInstancePatchMerge(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()
	mic := cf.NewManagedInstancesClient()

	poller, err := mic.BeginCreateOrUpdate(ctx, "rg-1", "mi1", armsql.ManagedInstance{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ManagedInstanceProperties{
			AdministratorLogin: to.Ptr("miadmin"),
			VCores:             to.Ptr(int32(4)),
			SubnetID:           to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vn/subnets/mi"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("MI create: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MI create poll: %v", err)
	}

	// PATCH only vCores — administratorLogin must survive the merge.
	up, err := mic.BeginUpdate(ctx, "rg-1", "mi1", armsql.ManagedInstanceUpdate{
		Properties: &armsql.ManagedInstanceProperties{VCores: to.Ptr(int32(8))},
	}, nil)
	if err != nil {
		t.Fatalf("MI BeginUpdate: %v", err)
	}

	if _, err := up.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("MI patch: %v", err)
	}

	got, err := mic.Get(ctx, "rg-1", "mi1", nil)
	if err != nil {
		t.Fatalf("MI Get: %v", err)
	}

	if got.Properties == nil || got.Properties.VCores == nil || *got.Properties.VCores != 8 {
		t.Fatalf("PATCH did not apply vCores: %v", got.Properties)
	}

	if got.Properties.AdministratorLogin == nil || *got.Properties.AdministratorLogin != "miadmin" {
		t.Fatalf("PATCH wiped administratorLogin: %v", got.Properties)
	}
}
