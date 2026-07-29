package mysqlflex_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

func mustCreateServer(t *testing.T, cf *armmysqlflexibleservers.ClientFactory) {
	t.Helper()

	ctx := context.Background()

	poller, err := cf.NewServersClient().BeginCreate(ctx, "rg-1", "srv1", armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}
}

func TestSDKMySQLFlexDatabases(t *testing.T) {
	cf := newFactory(t)
	mustCreateServer(t, cf)

	ctx := context.Background()
	dbs := cf.NewDatabasesClient()

	poller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armmysqlflexibleservers.Database{
		Properties: &armmysqlflexibleservers.DatabaseProperties{
			Charset:   to.Ptr("utf8mb4"),
			Collation: to.Ptr("utf8mb4_unicode_ci"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("db create PollUntilDone: %v", err)
	}

	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Charset == nil || *got.Properties.Charset != "utf8mb4" {
		t.Fatalf("charset: got %v, want utf8mb4", got.Properties)
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

func TestSDKMySQLFlexFirewallRules(t *testing.T) {
	cf := newFactory(t)
	mustCreateServer(t, cf)

	ctx := context.Background()
	fw := cf.NewFirewallRulesClient()

	poller, err := fw.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "office", armmysqlflexibleservers.FirewallRule{
		Properties: &armmysqlflexibleservers.FirewallRuleProperties{
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

func TestSDKMySQLFlexConfigurations(t *testing.T) {
	cf := newFactory(t)
	mustCreateServer(t, cf)

	ctx := context.Background()
	conf := cf.NewConfigurationsClient()

	poller, err := conf.BeginUpdate(ctx, "rg-1", "srv1", "max_connections", armmysqlflexibleservers.Configuration{
		Properties: &armmysqlflexibleservers.ConfigurationProperties{
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

	batchPoller, err := conf.BeginBatchUpdate(ctx, "rg-1", "srv1", armmysqlflexibleservers.ConfigurationListForBatchUpdate{
		Value: []*armmysqlflexibleservers.ConfigurationForBatchUpdate{
			{Name: to.Ptr("slow_query_log"), Properties: &armmysqlflexibleservers.ConfigurationForBatchUpdateProperties{
				Value: to.Ptr("ON"),
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginBatchUpdate: %v", err)
	}

	if _, err := batchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("batch PollUntilDone: %v", err)
	}

	pager := conf.NewListByServerPager("rg-1", "srv1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 2 {
		t.Fatalf("got %d configurations, want 2", len(page.Value))
	}
}

func TestSDKMySQLFlexFailover(t *testing.T) {
	cf := newFactory(t)
	mustCreateServer(t, cf)

	ctx := context.Background()
	servers := cf.NewServersClient()

	poller, err := servers.BeginFailover(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("BeginFailover: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("failover PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("Get after failover: %v", err)
	}

	if got.Server.Properties == nil || got.Server.Properties.State == nil ||
		*got.Server.Properties.State != armmysqlflexibleservers.ServerStateReady {
		t.Fatalf("expected Ready after failover, got %v", got.Server.Properties.State)
	}
}
