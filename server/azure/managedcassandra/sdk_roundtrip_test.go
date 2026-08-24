package managedcassandra_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const subID = "sub-123"

func deref(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newFactory(t *testing.T) *armcosmos.ClientFactory {
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

	f, err := armcosmos.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("client factory: %v", err)
	}

	return f
}

func lroCtx(t *testing.T) context.Context {
	t.Helper()

	return context.Background()
}

func TestSDKClusterLifecycle(t *testing.T) {
	f := newFactory(t)
	cc := f.NewCassandraClustersClient()
	ctx := lroCtx(t)

	poller, err := cc.BeginCreateUpdate(ctx, "rg1", "cass", armcosmos.ClusterResource{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armcosmos.ClusterResourceProperties{
			DelegatedManagementSubnetID: to.Ptr("/subnets/sn"),
			CassandraVersion:            to.Ptr("3.11"),
			RepairEnabled:               to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if *created.Name != "cass" || *created.Properties.ProvisioningState != armcosmos.ManagedCassandraProvisioningStateSucceeded {
		t.Fatalf("cluster wrong: name=%v state=%v", deref(created.Name), created.Properties.ProvisioningState)
	}

	got, err := cc.Get(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if deref(got.Properties.CassandraVersion) != "3.11" || !*got.Properties.RepairEnabled {
		t.Fatalf("get props wrong: %+v", got.Properties)
	}

	// List in RG.
	pager := cc.NewListByResourceGroupPager("rg1", nil)
	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("list by rg: got %d, want 1", len(page.Value))
	}

	// Delete.
	del, err := cc.BeginDelete(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := del.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	if _, err := cc.Get(ctx, "rg1", "cass", nil); err == nil {
		t.Fatal("get after delete: expected error")
	}
}

func TestSDKDataCentersAndActions(t *testing.T) {
	f := newFactory(t)
	cc := f.NewCassandraClustersClient()
	dcc := f.NewCassandraDataCentersClient()
	ctx := lroCtx(t)

	mustCreateCluster(t, cc, ctx)

	// Create a datacenter.
	dcPoller, err := dcc.BeginCreateUpdate(ctx, "rg1", "cass", "dc1", armcosmos.DataCenterResource{
		Properties: &armcosmos.DataCenterResourceProperties{
			DataCenterLocation: to.Ptr("eastus"),
			DelegatedSubnetID:  to.Ptr("/subnets/dc"),
			NodeCount:          to.Ptr[int32](3),
		},
	}, nil)
	if err != nil {
		t.Fatalf("dc BeginCreateUpdate: %v", err)
	}

	dc, err := dcPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("dc create poll: %v", err)
	}

	if *dc.Properties.NodeCount != 3 || len(dc.Properties.SeedNodes) != 3 {
		t.Fatalf("datacenter wrong: %+v", dc.Properties)
	}

	// List datacenters.
	pager := dcc.NewListPager("rg1", "cass", nil)
	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("dc list: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("dc list: got %d, want 1", len(page.Value))
	}

	// Deallocate → status shows STOPPED.
	dePoller, err := cc.BeginDeallocate(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("BeginDeallocate: %v", err)
	}

	if _, err := dePoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("deallocate poll: %v", err)
	}

	status, err := cc.Status(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(status.DataCenters) != 1 || len(status.DataCenters[0].Nodes) != 3 {
		t.Fatalf("status shape wrong: %+v", status.DataCenters)
	}

	// InvokeCommand.
	invPoller, err := cc.BeginInvokeCommand(ctx, "rg1", "cass", armcosmos.CommandPostBody{
		Command: to.Ptr("nodetool status"), Host: to.Ptr("10.0.0.4"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginInvokeCommand: %v", err)
	}

	out, err := invPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("invoke poll: %v", err)
	}

	if deref(out.CommandOutput.CommandOutput) == "" {
		t.Fatal("expected command output")
	}
}

func TestSDKUpdateListAndErrors(t *testing.T) {
	f := newFactory(t)
	cc := f.NewCassandraClustersClient()
	dcc := f.NewCassandraDataCentersClient()
	ctx := lroCtx(t)

	mustCreateCluster(t, cc, ctx)

	// PATCH the cluster.
	upPoller, err := cc.BeginUpdate(ctx, "rg1", "cass", armcosmos.ClusterResource{
		Tags: map[string]*string{"tier": to.Ptr("gold")},
		Properties: &armcosmos.ClusterResourceProperties{
			HoursBetweenBackups: to.Ptr[int32](12),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	updated, err := upPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update poll: %v", err)
	}

	if deref(updated.Tags["tier"]) != "gold" || *updated.Properties.HoursBetweenBackups != 12 {
		t.Fatalf("patch not applied: tags=%v hbb=%v", updated.Tags, updated.Properties.HoursBetweenBackups)
	}

	// List by subscription.
	subPager := cc.NewListBySubscriptionPager(nil)

	subPage, err := subPager.NextPage(ctx)
	if err != nil {
		t.Fatalf("list by sub: %v", err)
	}

	if len(subPage.Value) != 1 {
		t.Fatalf("list by sub: got %d, want 1", len(subPage.Value))
	}

	// Datacenter create → update (scale) → get → delete.
	mustCreateDataCenter(t, dcc, ctx)

	dcUp, err := dcc.BeginUpdate(ctx, "rg1", "cass", "dc1", armcosmos.DataCenterResource{
		Properties: &armcosmos.DataCenterResourceProperties{NodeCount: to.Ptr[int32](9)},
	}, nil)
	if err != nil {
		t.Fatalf("dc BeginUpdate: %v", err)
	}

	dc, err := dcUp.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("dc update poll: %v", err)
	}

	if *dc.Properties.NodeCount != 9 {
		t.Fatalf("dc scale not applied: %d", *dc.Properties.NodeCount)
	}

	if _, err := dcc.Get(ctx, "rg1", "cass", "dc1", nil); err != nil {
		t.Fatalf("dc Get: %v", err)
	}

	dcDel, err := dcc.BeginDelete(ctx, "rg1", "cass", "dc1", nil)
	if err != nil {
		t.Fatalf("dc BeginDelete: %v", err)
	}

	if _, err := dcDel.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("dc delete poll: %v", err)
	}

	// Get a missing cluster → error.
	if _, err := cc.Get(ctx, "rg1", "ghost", nil); err == nil {
		t.Fatal("get missing cluster: expected error")
	}
}

func TestSDKTypedErrorsAndStart(t *testing.T) {
	f := newFactory(t)
	cc := f.NewCassandraClustersClient()
	dcc := f.NewCassandraDataCentersClient()
	ctx := lroCtx(t)

	// Get a missing cluster → typed 404 ResourceError.
	_, err := cc.Get(ctx, "rg1", "ghost", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("get missing cluster: got %v, want 404 ResponseError", err)
	}

	// Datacenter create under a missing cluster → typed 404 ParentResourceNotFound.
	dcPoller, err := dcc.BeginCreateUpdate(ctx, "rg1", "ghost", "dc1", armcosmos.DataCenterResource{
		Properties: &armcosmos.DataCenterResourceProperties{NodeCount: to.Ptr[int32](3)},
	}, nil)
	if err == nil {
		_, err = dcPoller.PollUntilDone(ctx, nil)
	}

	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("dc under missing cluster: got %v, want 404 ResponseError", err)
	}

	if respErr.ErrorCode != "ParentResourceNotFound" {
		t.Fatalf("dc under missing cluster: got code %q, want ParentResourceNotFound", respErr.ErrorCode)
	}

	// Deallocate → Start round-trips (Start shares the async path).
	mustCreateCluster(t, cc, ctx)

	dePoller, err := cc.BeginDeallocate(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("BeginDeallocate: %v", err)
	}

	if _, err := dePoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("deallocate poll: %v", err)
	}

	stPoller, err := cc.BeginStart(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	if _, err := stPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("start poll: %v", err)
	}

	got, err := cc.Get(ctx, "rg1", "cass", nil)
	if err != nil {
		t.Fatalf("Get after start: %v", err)
	}

	if got.Properties.Deallocated != nil && *got.Properties.Deallocated {
		t.Fatal("cluster still deallocated after start")
	}
}

func TestMalformedBodyRejected(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewServer(azureserver.NewFromProvider(cloudP))
	t.Cleanup(ts.Close)

	url := ts.URL + "/subscriptions/" + subID +
		"/resourceGroups/rg1/providers/Microsoft.DocumentDB/cassandraClusters/cass?api-version=2024-11-15"

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

func mustCreateDataCenter(t *testing.T, dcc *armcosmos.CassandraDataCentersClient, ctx context.Context) {
	t.Helper()

	poller, err := dcc.BeginCreateUpdate(ctx, "rg1", "cass", "dc1", armcosmos.DataCenterResource{
		Properties: &armcosmos.DataCenterResourceProperties{
			DataCenterLocation: to.Ptr("eastus"), NodeCount: to.Ptr[int32](3),
		},
	}, nil)
	if err != nil {
		t.Fatalf("dc create: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("dc create poll: %v", err)
	}
}

func mustCreateCluster(t *testing.T, cc *armcosmos.CassandraClustersClient, ctx context.Context) {
	t.Helper()

	poller, err := cc.BeginCreateUpdate(ctx, "rg1", "cass", armcosmos.ClusterResource{
		Location: to.Ptr("eastus"),
		Properties: &armcosmos.ClusterResourceProperties{
			DelegatedManagementSubnetID: to.Ptr("/subnets/sn"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create cluster poll: %v", err)
	}
}
