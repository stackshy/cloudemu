package azureai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func mlBase() string {
	return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/Microsoft.MachineLearningServices"
}

func newMLServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MachineLearning: cloud.AzureAI})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func TestMLWorkspaceLifecycle(t *testing.T) {
	url := newMLServer(t)
	ws := mlBase() + "/workspaces/ws1"

	created := do(t, http.MethodPut, url+ws, map[string]any{
		"location": "eastus", "kind": "Hub",
		"properties": map[string]any{"friendlyName": "Hub WS"},
	})
	assert.Equal(t, "ws1", created["name"])
	assert.Equal(t, "Hub", created["kind"])
	assert.Equal(t, "Succeeded", props(created)["provisioningState"])

	got := do(t, http.MethodGet, url+ws, nil)
	assert.Equal(t, "Hub WS", props(got)["friendlyName"])

	list := do(t, http.MethodGet, url+mlBase()+"/workspaces", nil)
	assert.Len(t, list["value"], 1)
}

func TestMLComputeLifecycle(t *testing.T) {
	url := newMLServer(t)
	do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1", map[string]any{"location": "eastus"})

	cBase := mlBase() + "/workspaces/ws1/computes/cpu"
	c := do(t, http.MethodPut, url+cBase, map[string]any{
		"properties": map[string]any{"computeType": "AmlCompute", "properties": map[string]any{"vmSize": "STANDARD_DS3_V2"}},
	})
	assert.Equal(t, "cpu", c["name"])

	// stop then start lifecycle.
	do(t, http.MethodPost, url+cBase+"/stop", map[string]any{})
	stopped := do(t, http.MethodGet, url+cBase, nil)
	assert.Equal(t, "Stopped", props(props(stopped))["state"])
	do(t, http.MethodPost, url+cBase+"/start", map[string]any{})

	list := do(t, http.MethodGet, url+mlBase()+"/workspaces/ws1/computes", nil)
	assert.Len(t, list["value"], 1)
}

func TestMLOnlineEndpointAndDeployment(t *testing.T) {
	url := newMLServer(t)
	do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1", map[string]any{"location": "eastus"})

	epBase := mlBase() + "/workspaces/ws1/onlineEndpoints/ep1"
	ep := do(t, http.MethodPut, url+epBase, map[string]any{"properties": map[string]any{"authMode": "Key"}})
	assert.Equal(t, "ep1", ep["name"])
	assert.NotEmpty(t, props(ep)["scoringUri"])

	dep := do(t, http.MethodPut, url+epBase+"/deployments/blue", map[string]any{
		"properties": map[string]any{"model": "azureml:m:1", "instanceType": "Standard_DS3_v2"},
		"sku":        map[string]any{"capacity": 3},
	})
	assert.Equal(t, "blue", dep["name"])

	deps := do(t, http.MethodGet, url+epBase+"/deployments", nil)
	assert.Len(t, deps["value"], 1)

	eps := do(t, http.MethodGet, url+mlBase()+"/workspaces/ws1/onlineEndpoints", nil)
	assert.Len(t, eps["value"], 1)
}

func TestMLJobsAndAssets(t *testing.T) {
	url := newMLServer(t)
	do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1", map[string]any{"location": "eastus"})

	job := do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1/jobs/j1", map[string]any{
		"properties": map[string]any{"jobType": "Command", "displayName": "train"},
	})
	assert.Equal(t, "Completed", props(job)["status"])
	do(t, http.MethodPost, url+mlBase()+"/workspaces/ws1/jobs/j1/cancel", map[string]any{})

	// Versioned model asset.
	mv := mlBase() + "/workspaces/ws1/models/m/versions/1"
	asset := do(t, http.MethodPut, url+mv, map[string]any{
		"properties": map[string]any{"description": "v1", "path": "azureml://models/m/1"},
	})
	assert.Equal(t, "1", asset["name"])

	versions := do(t, http.MethodGet, url+mlBase()+"/workspaces/ws1/models/m/versions", nil)
	assert.Len(t, versions["value"], 1)

	containers := do(t, http.MethodGet, url+mlBase()+"/workspaces/ws1/models", nil)
	assert.Len(t, containers["value"], 1)
}

func TestMLAssetLatestVersionNumeric(t *testing.T) {
	url := newMLServer(t)
	do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1", map[string]any{"location": "eastus"})

	// Register versions 1..10; lexical compare would report "9" as latest.
	for _, v := range []string{"1", "2", "9", "10"} {
		do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1/models/m/versions/"+v, map[string]any{
			"properties": map[string]any{"path": "azureml://models/m/" + v},
		})
	}

	containers := do(t, http.MethodGet, url+mlBase()+"/workspaces/ws1/models", nil)
	require.Len(t, containers["value"], 1)
	c := containers["value"].([]any)[0].(map[string]any)
	assert.Equal(t, "10", c["properties"].(map[string]any)["latestVersion"])
}

func TestMLDatastoresConnectionsSchedulesRegistries(t *testing.T) {
	url := newMLServer(t)
	do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1", map[string]any{"location": "eastus"})

	ds := do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1/datastores/store1", map[string]any{
		"properties": map[string]any{"datastoreType": "AzureBlob", "accountName": "acct", "containerName": "data"},
	})
	assert.Equal(t, "store1", ds["name"])

	conn := do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1/connections/aoai", map[string]any{
		"properties": map[string]any{"category": "AzureOpenAI", "target": "https://x.openai.azure.com"},
	})
	assert.Equal(t, "aoai", conn["name"])

	sched := do(t, http.MethodPut, url+mlBase()+"/workspaces/ws1/schedules/nightly", map[string]any{
		"properties": map[string]any{"displayName": "nightly", "trigger": map[string]any{"expression": "0 0 * * *"}},
	})
	assert.Equal(t, "nightly", sched["name"])

	reg := do(t, http.MethodPut, url+mlBase()+"/registries/reg1", map[string]any{"location": "eastus"})
	assert.Equal(t, "reg1", reg["name"])

	regs := do(t, http.MethodGet, url+mlBase()+"/registries", nil)
	assert.Len(t, regs["value"], 1)

	require.NotEmpty(t, ds["id"])
}

// --- real armmachinelearning SDK roundtrip ---

func newMLClientFactory(t *testing.T) *armmachinelearning.ClientFactory {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MachineLearning: cloudP.AzureAI})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	cf, err := armmachinelearning.NewClientFactory(sub, fakeCred{}, armClientOptions(ts))
	require.NoError(t, err)

	return cf
}

func TestSDKMachineLearningWorkspaceAndAssets(t *testing.T) {
	cf := newMLClientFactory(t)
	ctx := context.Background()
	ws := cf.NewWorkspacesClient()

	poller, err := ws.BeginCreateOrUpdate(ctx, rg, "ws1", armmachinelearning.Workspace{
		Location: to.Ptr("eastus"),
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, created.Workspace.Name)
	assert.Equal(t, "ws1", *created.Workspace.Name)

	got, err := ws.Get(ctx, rg, "ws1", nil)
	require.NoError(t, err)
	assert.Equal(t, "eastus", *got.Workspace.Location)

	// Register versions 1, 2, 10; the latest must be the numeric max, not "9"/lexical.
	mv := cf.NewModelVersionsClient()
	for _, v := range []string{"1", "2", "10"} {
		_, cerr := mv.CreateOrUpdate(ctx, rg, "ws1", "m", v, armmachinelearning.ModelVersion{
			Properties: &armmachinelearning.ModelVersionProperties{Description: to.Ptr("v" + v)},
		}, nil)
		require.NoError(t, cerr)
	}

	versions := mv.NewListPager(rg, "ws1", "m", nil)
	vpage, err := versions.NextPage(ctx)
	require.NoError(t, err)
	assert.Len(t, vpage.Value, 3)

	mc := cf.NewModelContainersClient()
	cpage, err := mc.NewListPager(rg, "ws1", nil).NextPage(ctx)
	require.NoError(t, err)
	require.Len(t, cpage.Value, 1)
	require.NotNil(t, cpage.Value[0].Properties)
	require.NotNil(t, cpage.Value[0].Properties.LatestVersion)
	assert.Equal(t, "10", *cpage.Value[0].Properties.LatestVersion)

	page, err := ws.NewListByResourceGroupPager(rg, nil).NextPage(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(page.Value), 1)

	delPoller, err := ws.BeginDelete(ctx, rg, "ws1", nil)
	require.NoError(t, err)
	_, err = delPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
}
