package cosmospostgresql_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmosforpostgresql/armcosmosforpostgresql"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const subID = "sub-123"

func deref[T any](p *T) (v T) {
	if p != nil {
		return *p
	}

	return v
}

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newFactory(t *testing.T) *armcosmosforpostgresql.ClientFactory {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.NewFromProvider(cloudP))
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Cloud: myCloud, Transport: ts.Client()},
	}

	f, err := armcosmosforpostgresql.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("client factory: %v", err)
	}

	return f
}

func mustCreateCluster(t *testing.T, cc *armcosmosforpostgresql.ClustersClient, ctx context.Context, nodeCount int32) {
	t.Helper()

	poller, err := cc.BeginCreate(ctx, "rg1", "pg1", armcosmosforpostgresql.Cluster{
		Location: to.Ptr("eastus"),
		Properties: &armcosmosforpostgresql.ClusterProperties{
			AdministratorLoginPassword: to.Ptr("Sup3rSecret!"),
			CoordinatorVCores:          to.Ptr[int32](4),
			NodeCount:                  to.Ptr(nodeCount),
			CitusVersion:               to.Ptr("12.1"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create poll: %v", err)
	}
}

func TestSDKClusterLifecycle(t *testing.T) {
	f := newFactory(t)
	cc := f.NewClustersClient()
	ctx := context.Background()

	poller, err := cc.BeginCreate(ctx, "rg1", "pg1", armcosmosforpostgresql.Cluster{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armcosmosforpostgresql.ClusterProperties{
			AdministratorLoginPassword: to.Ptr("Sup3rSecret!"),
			NodeCount:                  to.Ptr[int32](2),
			CitusVersion:               to.Ptr("12.1"),
			EnableHa:                   to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if deref(created.Name) != "pg1" || deref(created.Properties.ProvisioningState) != "Succeeded" {
		t.Fatalf("created wrong: name=%q state=%q", deref(created.Name), deref(created.Properties.ProvisioningState))
	}

	if deref(created.Properties.NodeCount) != 2 || !deref(created.Properties.EnableHa) {
		t.Fatalf("created props wrong: %+v", created.Properties)
	}

	got, err := cc.Get(ctx, "rg1", "pg1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if deref(got.Properties.CitusVersion) != "12.1" {
		t.Fatalf("get props wrong: %+v", got.Properties)
	}

	pager := cc.NewListByResourceGroupPager("rg1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("list by rg: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("list by rg: got %d, want 1", len(page.Value))
	}

	del, err := cc.BeginDelete(ctx, "rg1", "pg1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := del.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	if _, err := cc.Get(ctx, "rg1", "pg1", nil); err == nil {
		t.Fatal("get after delete: expected error")
	}
}

func TestSDKUpdateActionsAndList(t *testing.T) {
	f := newFactory(t)
	cc := f.NewClustersClient()
	ctx := context.Background()

	mustCreateCluster(t, cc, ctx, 2)

	// PATCH: scale nodes + tags.
	up, err := cc.BeginUpdate(ctx, "rg1", "pg1", armcosmosforpostgresql.ClusterForUpdate{
		Tags:       map[string]*string{"tier": to.Ptr("gold")},
		Properties: &armcosmosforpostgresql.ClusterPropertiesForUpdate{NodeCount: to.Ptr[int32](4)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	updated, err := up.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update poll: %v", err)
	}

	if deref(updated.Properties.NodeCount) != 4 || deref(updated.Tags["tier"]) != "gold" {
		t.Fatalf("patch not applied: %+v tags=%v", updated.Properties, updated.Tags)
	}

	// List by subscription.
	subPager := cc.NewListPager(nil)

	subPage, err := subPager.NextPage(ctx)
	if err != nil {
		t.Fatalf("list by sub: %v", err)
	}

	if len(subPage.Value) != 1 {
		t.Fatalf("list by sub: got %d, want 1", len(subPage.Value))
	}

	// Stop / start / restart round-trip (Location LRO).
	for _, action := range []func() error{
		func() error { p, e := cc.BeginStop(ctx, "rg1", "pg1", nil); return pollErr(ctx, p, e) },
		func() error { p, e := cc.BeginStart(ctx, "rg1", "pg1", nil); return pollErr(ctx, p, e) },
		func() error { p, e := cc.BeginRestart(ctx, "rg1", "pg1", nil); return pollErr(ctx, p, e) },
	} {
		if err := action(); err != nil {
			t.Fatalf("cluster action: %v", err)
		}
	}
}

func pollErr[T any](ctx context.Context, p *runtime.Poller[T], err error) error {
	if err != nil {
		return err
	}

	_, err = p.PollUntilDone(ctx, nil)

	return err
}

func TestSDKFirewallRulesRolesServers(t *testing.T) {
	f := newFactory(t)
	cc := f.NewClustersClient()
	ctx := context.Background()

	mustCreateCluster(t, cc, ctx, 2)

	// Firewall rule.
	fwc := f.NewFirewallRulesClient()

	fwPoller, err := fwc.BeginCreateOrUpdate(ctx, "rg1", "pg1", "allow-all", armcosmosforpostgresql.FirewallRule{
		Properties: &armcosmosforpostgresql.FirewallRuleProperties{
			StartIPAddress: to.Ptr("0.0.0.0"), EndIPAddress: to.Ptr("255.255.255.255"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("fw BeginCreateOrUpdate: %v", err)
	}

	fw, err := fwPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("fw poll: %v", err)
	}

	if deref(fw.Properties.EndIPAddress) != "255.255.255.255" {
		t.Fatalf("fw props wrong: %+v", fw.Properties)
	}

	fwPage, err := fwc.NewListByClusterPager("rg1", "pg1", nil).NextPage(ctx)
	if err != nil || len(fwPage.Value) != 1 {
		t.Fatalf("fw list: %v len=%d", err, len(fwPage.Value))
	}

	// Role.
	rc := f.NewRolesClient()

	rolePoller, err := rc.BeginCreate(ctx, "rg1", "pg1", "app", armcosmosforpostgresql.Role{
		Properties: &armcosmosforpostgresql.RoleProperties{Password: to.Ptr("R0lePass!")},
	}, nil)
	if err != nil {
		t.Fatalf("role BeginCreate: %v", err)
	}

	if _, err := rolePoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("role poll: %v", err)
	}

	if _, err := rc.Get(ctx, "rg1", "pg1", "app", nil); err != nil {
		t.Fatalf("role Get: %v", err)
	}

	// Servers (derived nodes): one coordinator + two workers.
	sc := f.NewServersClient()

	srvPage, err := sc.NewListByClusterPager("rg1", "pg1", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("server list: %v", err)
	}

	if len(srvPage.Value) != 3 {
		t.Fatalf("server list: got %d, want 3", len(srvPage.Value))
	}

	if _, err := sc.Get(ctx, "rg1", "pg1", "pg1-c", nil); err != nil {
		t.Fatalf("server Get coordinator: %v", err)
	}
}

func TestSDKConfigurationsAndReplicaAndErrors(t *testing.T) {
	f := newFactory(t)
	cc := f.NewClustersClient()
	ctx := context.Background()

	mustCreateCluster(t, cc, ctx, 1)

	// Configurations: list, get coordinator, update coordinator.
	cfgc := f.NewConfigurationsClient()

	cfgPage, err := cfgc.NewListByClusterPager("rg1", "pg1", nil).NextPage(ctx)
	if err != nil || len(cfgPage.Value) == 0 {
		t.Fatalf("config list: %v len=%d", err, len(cfgPage.Value))
	}

	coord, err := cfgc.GetCoordinator(ctx, "rg1", "pg1", "max_connections", nil)
	if err != nil || deref(coord.Properties.Value) != "300" {
		t.Fatalf("get coordinator config: %v %+v", err, coord.Properties)
	}

	upPoller, err := cfgc.BeginUpdateOnCoordinator(ctx, "rg1", "pg1", "max_connections", armcosmosforpostgresql.ServerConfiguration{
		Properties: &armcosmosforpostgresql.ServerConfigurationProperties{Value: to.Ptr("500")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdateOnCoordinator: %v", err)
	}

	updatedCfg, err := upPoller.PollUntilDone(ctx, nil)
	if err != nil || deref(updatedCfg.Properties.Value) != "500" {
		t.Fatalf("coordinator config update: %v %+v", err, updatedCfg.Properties)
	}

	// checkNameAvailability.
	na, err := cc.CheckNameAvailability(ctx, armcosmosforpostgresql.NameAvailabilityRequest{Name: to.Ptr("pg1")}, nil)
	if err != nil {
		t.Fatalf("CheckNameAvailability: %v", err)
	}

	if deref(na.NameAvailable) {
		t.Fatal("existing name should be unavailable")
	}

	// Typed 404 for a missing cluster.
	_, err = cc.Get(ctx, "rg1", "ghost", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("get missing cluster: got %v, want 404 ResponseError", err)
	}
}

func TestMalformedBodyRejected(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewServer(azureserver.NewFromProvider(cloudP))
	t.Cleanup(ts.Close)

	url := ts.URL + "/subscriptions/" + subID +
		"/resourceGroups/rg1/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/pg1?api-version=2023-03-02-preview"

	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed body: got %d, want 400", resp.StatusCode)
	}
}
