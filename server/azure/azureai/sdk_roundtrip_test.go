package azureai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// fakeCred is a no-op bearer-token credential for driving real ARM SDK clients
// against the in-memory server.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// armClientOptions points a real ARM client at the in-memory TLS server.
func armClientOptions(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

const (
	sub  = "sub-1"
	rg   = "rg-1"
	acct = "my-ai"
)

func base() string {
	return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/Microsoft.CognitiveServices/accounts"
}

func newServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CognitiveServices: cloud.AzureAI})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func do(t *testing.T, method, url string, body any) map[string]any {
	t.Helper()

	var rdr io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "method=%s url=%s body=%s", method, url, raw)

	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), "body=%s", raw)
	}

	return out
}

func props(m map[string]any) map[string]any {
	p, _ := m["properties"].(map[string]any)

	return p
}

func TestAccountLifecycle(t *testing.T) {
	url := newServer(t)

	acc := do(t, http.MethodPut, url+base()+"/"+acct, map[string]any{
		"location": "eastus", "kind": "AIServices", "sku": map[string]any{"name": "S0"},
		"tags": map[string]any{"env": "test"},
	})
	assert.Equal(t, acct, acc["name"])
	assert.NotEmpty(t, props(acc)["endpoint"])
	assert.Equal(t, "Succeeded", props(acc)["provisioningState"])

	got := do(t, http.MethodGet, url+base()+"/"+acct, nil)
	assert.Equal(t, "eastus", got["location"])

	patched := do(t, http.MethodPatch, url+base()+"/"+acct, map[string]any{"tags": map[string]any{"env": "prod"}})
	assert.Equal(t, "prod", patched["tags"].(map[string]any)["env"])

	list := do(t, http.MethodGet, url+base(), nil)
	assert.Len(t, list["value"], 1)

	subList := do(t, http.MethodGet,
		url+"/subscriptions/"+sub+"/providers/Microsoft.CognitiveServices/accounts", nil)
	assert.Len(t, subList["value"], 1)
}

func TestAccountKeysAndCatalogs(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+base()+"/"+acct, map[string]any{"location": "eastus", "kind": "OpenAI"})

	keys := do(t, http.MethodPost, url+base()+"/"+acct+"/listKeys", map[string]any{})
	require.NotEmpty(t, keys["key1"])
	require.NotEmpty(t, keys["key2"])

	regen := do(t, http.MethodPost, url+base()+"/"+acct+"/regenerateKey", map[string]any{"keyName": "Key1"})
	assert.NotEqual(t, keys["key1"], regen["key1"], "regenerated key1 must change")
	assert.Equal(t, keys["key2"], regen["key2"], "key2 must be stable")

	// The rotation must persist: a subsequent listKeys returns the new key1.
	after := do(t, http.MethodPost, url+base()+"/"+acct+"/listKeys", map[string]any{})
	assert.Equal(t, regen["key1"], after["key1"], "rotated key1 must persist")
	assert.Equal(t, keys["key2"], after["key2"])

	models := do(t, http.MethodGet, url+base()+"/"+acct+"/models", nil)
	assert.NotEmpty(t, models["value"])

	skus := do(t, http.MethodGet, url+base()+"/"+acct+"/skus", nil)
	assert.NotEmpty(t, skus["value"])

	usages := do(t, http.MethodGet, url+base()+"/"+acct+"/usages", nil)
	assert.NotEmpty(t, usages["value"])
}

func TestDeploymentsAndChildren(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+base()+"/"+acct, map[string]any{"location": "eastus", "kind": "OpenAI"})

	dep := do(t, http.MethodPut, url+base()+"/"+acct+"/deployments/gpt4o", map[string]any{
		"sku":        map[string]any{"name": "Standard", "capacity": 10},
		"properties": map[string]any{"model": map[string]any{"name": "gpt-4o", "version": "2024-08-06", "format": "OpenAI"}},
	})
	assert.Equal(t, "gpt4o", dep["name"])
	assert.Equal(t, "gpt-4o", props(dep)["model"].(map[string]any)["name"])

	got := do(t, http.MethodGet, url+base()+"/"+acct+"/deployments/gpt4o", nil)
	assert.Equal(t, "gpt4o", got["name"])

	depList := do(t, http.MethodGet, url+base()+"/"+acct+"/deployments", nil)
	assert.Len(t, depList["value"], 1)

	// Project (AI Foundry).
	proj := do(t, http.MethodPut, url+base()+"/"+acct+"/projects/proj1", map[string]any{
		"location": "eastus", "properties": map[string]any{"displayName": "My Project"},
	})
	assert.Equal(t, "My Project", props(proj)["displayName"])

	// RAI policy.
	rai := do(t, http.MethodPut, url+base()+"/"+acct+"/raiPolicies/strict", map[string]any{
		"properties": map[string]any{"mode": "Blocking"},
	})
	assert.Equal(t, "Blocking", props(rai)["mode"])

	// Commitment plan.
	cp := do(t, http.MethodPut, url+base()+"/"+acct+"/commitmentPlans/plan1", map[string]any{
		"properties": map[string]any{"planType": "PTU", "autoRenew": true},
	})
	assert.Equal(t, "PTU", props(cp)["planType"])

	// Private endpoint connection.
	pec := do(t, http.MethodPut, url+base()+"/"+acct+"/privateEndpointConnections/pec1", map[string]any{
		"properties": map[string]any{"privateLinkServiceConnectionState": map[string]any{"status": "Approved"}},
	})
	assert.Equal(t, "Approved", props(pec)["privateLinkServiceConnectionState"].(map[string]any)["status"])

	do(t, http.MethodDelete, url+base()+"/"+acct+"/deployments/gpt4o", nil)
	after := do(t, http.MethodGet, url+base()+"/"+acct+"/deployments", nil)
	assert.Empty(t, after["value"])
}

func TestAccountNotFound(t *testing.T) {
	url := newServer(t)

	req, _ := http.NewRequest(http.MethodGet, url+base()+"/ghost", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- real armcognitiveservices SDK roundtrip ---

func newCSAccountsClient(t *testing.T) *armcognitiveservices.AccountsClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CognitiveServices: cloudP.AzureAI})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	c, err := armcognitiveservices.NewAccountsClient(sub, fakeCred{}, armClientOptions(ts))
	require.NoError(t, err)

	return c
}

func TestSDKCognitiveServicesAccountLifecycle(t *testing.T) {
	c := newCSAccountsClient(t)
	ctx := context.Background()

	poller, err := c.BeginCreate(ctx, rg, acct, armcognitiveservices.Account{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr("AIServices"),
		SKU:      &armcognitiveservices.SKU{Name: to.Ptr("S0")},
	}, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, created.Account.Name)
	assert.Equal(t, acct, *created.Account.Name)

	got, err := c.Get(ctx, rg, acct, nil)
	require.NoError(t, err)
	assert.Equal(t, "eastus", *got.Account.Location)
	require.NotNil(t, got.Account.Properties)
	require.NotNil(t, got.Account.Properties.Endpoint)
	assert.NotEmpty(t, *got.Account.Properties.Endpoint)

	keys, err := c.ListKeys(ctx, rg, acct, nil)
	require.NoError(t, err)
	require.NotNil(t, keys.Key1)

	regen, err := c.RegenerateKey(ctx, rg, acct, armcognitiveservices.RegenerateKeyParameters{
		KeyName: to.Ptr(armcognitiveservices.KeyNameKey1),
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, regen.Key1)
	assert.NotEqual(t, *keys.Key1, *regen.Key1, "rotated key must differ")

	pager := c.NewListByResourceGroupPager(rg, nil)
	page, err := pager.NextPage(ctx)
	require.NoError(t, err)
	assert.Len(t, page.Value, 1)

	delPoller, err := c.BeginDelete(ctx, rg, acct, nil)
	require.NoError(t, err)
	_, err = delPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = c.Get(ctx, rg, acct, nil)
	assert.Error(t, err, "account should be gone after delete")
}
