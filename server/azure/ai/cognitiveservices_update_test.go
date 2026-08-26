package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newCSTLSServer starts an in-memory TLS server backed by the Azure AI mock.
func newCSTLSServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CognitiveServices: cloudP.AI})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// newCSDeploymentsClient builds a DeploymentsClient pointed at ts.
func newCSDeploymentsClient(t *testing.T, ts *httptest.Server) *armcognitiveservices.DeploymentsClient {
	t.Helper()

	c, err := armcognitiveservices.NewDeploymentsClient(sub, fakeCred{}, armClientOptions(ts))
	require.NoError(t, err)

	return c
}

// newCSAccountsClientOn builds an AccountsClient pointed at ts.
func newCSAccountsClientOn(t *testing.T, ts *httptest.Server) *armcognitiveservices.AccountsClient {
	t.Helper()

	c, err := armcognitiveservices.NewAccountsClient(sub, fakeCred{}, armClientOptions(ts))
	require.NoError(t, err)

	return c
}

// postStatus issues a raw POST and returns the HTTP status code.
func postStatus(t *testing.T, url string, body any) int {
	t.Helper()

	b, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	return resp.StatusCode
}

// TestSDKAccountPatchPreservesTags verifies a PATCH that changes a non-tag field
// (sku) neither wipes existing tags (the nil-mask bug) nor is a silent no-op.
func TestSDKAccountPatchPreservesTags(t *testing.T) {
	c := newCSAccountsClientOn(t, newCSTLSServer(t))
	ctx := context.Background()

	createPoller, err := c.BeginCreate(ctx, rg, acct, armcognitiveservices.Account{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr("AIServices"),
		SKU:      &armcognitiveservices.SKU{Name: to.Ptr("S0")},
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil)
	require.NoError(t, err)
	_, err = createPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	// PATCH the sku only; the Tags map is nil so the SDK omits it from the body.
	upPoller, err := c.BeginUpdate(ctx, rg, acct, armcognitiveservices.Account{
		SKU: &armcognitiveservices.SKU{Name: to.Ptr("S1")},
	}, nil)
	require.NoError(t, err)
	_, err = upPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := c.Get(ctx, rg, acct, nil)
	require.NoError(t, err)

	require.Contains(t, got.Account.Tags, "env", "existing tags must survive a non-tag PATCH")
	assert.Equal(t, "test", *got.Account.Tags["env"])
	require.NotNil(t, got.Account.SKU)
	assert.Equal(t, "S1", *got.Account.SKU.Name, "PATCH of sku must apply, not be a no-op")
}

// TestSDKDeploymentRaiPolicyRoundTrip verifies raiPolicyName and
// versionUpgradeOption survive a create->get round trip.
func TestSDKDeploymentRaiPolicyRoundTrip(t *testing.T) {
	ctx := context.Background()

	ts := newCSTLSServer(t)
	accClient := newCSAccountsClientOn(t, ts)
	createPoller, err := accClient.BeginCreate(ctx, rg, acct, armcognitiveservices.Account{
		Location: to.Ptr("eastus"), Kind: to.Ptr("OpenAI"),
	}, nil)
	require.NoError(t, err)
	_, err = createPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	depClient := newCSDeploymentsClient(t, ts)

	upgrade := armcognitiveservices.DeploymentModelVersionUpgradeOptionNoAutoUpgrade
	depPoller, err := depClient.BeginCreateOrUpdate(ctx, rg, acct, "gpt4o", armcognitiveservices.Deployment{
		SKU: &armcognitiveservices.SKU{Name: to.Ptr("Standard"), Capacity: to.Ptr[int32](10)},
		Properties: &armcognitiveservices.DeploymentProperties{
			Model: &armcognitiveservices.DeploymentModel{
				Name: to.Ptr("gpt-4o"), Version: to.Ptr("2024-08-06"), Format: to.Ptr("OpenAI"),
			},
			RaiPolicyName:        to.Ptr("Microsoft.DefaultV2"),
			VersionUpgradeOption: &upgrade,
		},
	}, nil)
	require.NoError(t, err)
	_, err = depPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := depClient.Get(ctx, rg, acct, "gpt4o", nil)
	require.NoError(t, err)
	require.NotNil(t, got.Deployment.Properties)
	require.NotNil(t, got.Deployment.Properties.RaiPolicyName)
	assert.Equal(t, "Microsoft.DefaultV2", *got.Deployment.Properties.RaiPolicyName)
	require.NotNil(t, got.Deployment.Properties.VersionUpgradeOption)
	assert.Equal(t, upgrade, *got.Deployment.Properties.VersionUpgradeOption)
}

// TestDeploymentVersionUpgradeOptionDefault verifies the default is applied when
// the request omits versionUpgradeOption.
func TestDeploymentVersionUpgradeOptionDefault(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+base()+"/"+acct, map[string]any{"location": "eastus", "kind": "OpenAI"})

	dep := do(t, http.MethodPut, url+base()+"/"+acct+"/deployments/gpt4o", map[string]any{
		"properties": map[string]any{"model": map[string]any{"name": "gpt-4o", "version": "2024-08-06", "format": "OpenAI"}},
	})
	assert.Equal(t, "OnceNewDefaultVersionAvailable", props(dep)["versionUpgradeOption"])
}

// TestSDKAccountCustomSubDomainEndpoint verifies the synthesized endpoint uses
// the customSubDomainName rather than the account name.
func TestSDKAccountCustomSubDomainEndpoint(t *testing.T) {
	c := newCSAccountsClientOn(t, newCSTLSServer(t))
	ctx := context.Background()

	createPoller, err := c.BeginCreate(ctx, rg, acct, armcognitiveservices.Account{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr("OpenAI"),
		Properties: &armcognitiveservices.AccountProperties{
			CustomSubDomainName: to.Ptr("mycustomdomain"),
		},
	}, nil)
	require.NoError(t, err)
	_, err = createPoller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	got, err := c.Get(ctx, rg, acct, nil)
	require.NoError(t, err)
	require.NotNil(t, got.Account.Properties)
	require.NotNil(t, got.Account.Properties.Endpoint)
	assert.Equal(t, "https://mycustomdomain.openai.azure.com/", *got.Account.Properties.Endpoint)
	assert.NotContains(t, *got.Account.Properties.Endpoint, acct)
}

// TestRegenerateKeyBadKeyName verifies an invalid keyName is rejected with 400
// rather than silently rotating Key1.
func TestRegenerateKeyBadKeyName(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+base()+"/"+acct, map[string]any{"location": "eastus", "kind": "OpenAI"})

	for _, keyName := range []string{"", "Key3", "primary"} {
		status := postStatus(t, url+base()+"/"+acct+"/regenerateKey", map[string]any{"keyName": keyName})
		assert.Equalf(t, http.StatusBadRequest, status, "keyName=%q must be rejected", keyName)
	}
}

func TestRegenerateKeyKey2(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+base()+"/"+acct, map[string]any{"location": "eastus", "kind": "OpenAI"})

	keys := do(t, http.MethodPost, url+base()+"/"+acct+"/listKeys", map[string]any{})
	regen := do(t, http.MethodPost, url+base()+"/"+acct+"/regenerateKey", map[string]any{"keyName": "Key2"})
	assert.NotEqual(t, keys["key2"], regen["key2"], "regenerated key2 must change")
	assert.Equal(t, keys["key1"], regen["key1"], "key1 must be stable")
}
