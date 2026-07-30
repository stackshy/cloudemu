package postgresflex_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

func mustCreateServer(t *testing.T, opts *arm.ClientOptions) {
	t.Helper()

	ctx := context.Background()

	servers, err := armpostgresqlflexibleservers.NewServersClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewServersClient: %v", err)
	}

	poller, err := servers.BeginCreate(ctx, "rg-1", "srv1", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}
}

func TestSDKPostgresFlexDatabases(t *testing.T) {
	opts := newClientOpts(t)
	mustCreateServer(t, opts)

	ctx := context.Background()

	dbs, err := armpostgresqlflexibleservers.NewDatabasesClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewDatabasesClient: %v", err)
	}

	poller, err := dbs.BeginCreate(ctx, "rg-1", "srv1", "appdb", armpostgresqlflexibleservers.Database{
		Properties: &armpostgresqlflexibleservers.DatabaseProperties{
			Charset:   to.Ptr("UTF8"),
			Collation: to.Ptr("en_US.utf8"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db create PollUntilDone: %v", err)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Charset == nil || *got.Properties.Charset != "UTF8" {
		t.Fatalf("charset: got %v, want UTF8", got.Properties)
	}

	pager := dbs.NewListByServerPager("rg-1", "srv1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d databases, want 1", len(page.Value))
	}

	delPoller, err := dbs.BeginDelete(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db delete PollUntilDone: %v", err)
	}

	if _, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil); err == nil {
		t.Fatal("expected NotFound after database delete")
	}
}

func TestSDKPostgresFlexFirewallRules(t *testing.T) {
	opts := newClientOpts(t)
	mustCreateServer(t, opts)

	ctx := context.Background()

	fw, err := armpostgresqlflexibleservers.NewFirewallRulesClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewFirewallRulesClient: %v", err)
	}

	poller, err := fw.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "office", armpostgresqlflexibleservers.FirewallRule{
		Properties: &armpostgresqlflexibleservers.FirewallRuleProperties{
			StartIPAddress: to.Ptr("10.0.0.1"),
			EndIPAddress:   to.Ptr("10.0.0.255"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("fw create PollUntilDone: %v", err)
	}

	got, err := fw.Get(ctx, "rg-1", "srv1", "office", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.StartIPAddress == nil || *got.Properties.StartIPAddress != "10.0.0.1" {
		t.Fatalf("start ip: got %v, want 10.0.0.1", got.Properties)
	}

	pager := fw.NewListByServerPager("rg-1", "srv1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d rules, want 1", len(page.Value))
	}

	delPoller, err := fw.BeginDelete(ctx, "rg-1", "srv1", "office", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("fw delete PollUntilDone: %v", err)
	}

	if _, err := fw.Get(ctx, "rg-1", "srv1", "office", nil); err == nil {
		t.Fatal("expected NotFound after firewall rule delete")
	}
}

func TestSDKPostgresFlexConfigurations(t *testing.T) {
	opts := newClientOpts(t)
	mustCreateServer(t, opts)

	ctx := context.Background()

	conf, err := armpostgresqlflexibleservers.NewConfigurationsClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewConfigurationsClient: %v", err)
	}

	poller, err := conf.BeginUpdate(ctx, "rg-1", "srv1", "max_connections", armpostgresqlflexibleservers.Configuration{
		Properties: &armpostgresqlflexibleservers.ConfigurationProperties{
			Value: to.Ptr("200"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("config update PollUntilDone: %v", err)
	}

	got, err := conf.Get(ctx, "rg-1", "srv1", "max_connections", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Value == nil || *got.Properties.Value != "200" {
		t.Fatalf("value: got %v, want 200", got.Properties)
	}

	pager := conf.NewListByServerPager("rg-1", "srv1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// List returns the full parameter catalog with the override applied.
	if len(page.Value) < 1 {
		t.Fatalf("got %d configurations, want the catalog", len(page.Value))
	}

	var sawOverride bool
	for _, c := range page.Value {
		if c.Name != nil && *c.Name == "max_connections" &&
			c.Properties != nil && c.Properties.Value != nil && *c.Properties.Value == "200" {
			sawOverride = true
		}
	}

	if !sawOverride {
		t.Error("max_connections override missing from catalog list")
	}
}
