package azuresearch_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// fakeCred is a no-op bearer-token credential for driving real ARM SDK clients.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

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
	svcN = "mysearch"
)

func armBase() string {
	return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/Microsoft.Search/searchServices"
}

func newServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		SearchControl:   cloud.AzureSearch,
		SearchDataPlane: cloud.AzureSearch,
	})
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
	if len(bytes.TrimSpace(raw)) > 0 && bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		require.NoError(t, json.Unmarshal(raw, &out), "body=%s", raw)
	}

	return out
}

func props(m map[string]any) map[string]any {
	p, _ := m["properties"].(map[string]any)

	return p
}

func TestServiceLifecycleAndKeys(t *testing.T) {
	url := newServer(t)

	svc := do(t, http.MethodPut, url+armBase()+"/"+svcN, map[string]any{
		"location": "eastus", "sku": map[string]any{"name": "standard"},
		"properties": map[string]any{"replicaCount": 2, "partitionCount": 1},
	})
	assert.Equal(t, svcN, svc["name"])
	assert.NotEmpty(t, props(svc)["endpoint"])

	got := do(t, http.MethodGet, url+armBase()+"/"+svcN, nil)
	assert.EqualValues(t, 2, props(got)["replicaCount"])

	list := do(t, http.MethodGet, url+armBase(), nil)
	assert.Len(t, list["value"], 1)

	keys := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/listAdminKeys", map[string]any{})
	require.NotEmpty(t, keys["primaryKey"])

	regen := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/regenerateAdminKey/primary", map[string]any{})
	assert.NotEqual(t, keys["primaryKey"], regen["primaryKey"])

	qk := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/createQueryKey/reader", map[string]any{})
	require.NotEmpty(t, qk["key"])

	qkeys := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/listQueryKeys", map[string]any{})
	assert.GreaterOrEqual(t, len(qkeys["value"].([]any)), 2)
}

func TestSharedPrivateLinkAndPEC(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+armBase()+"/"+svcN, map[string]any{"location": "eastus"})

	spl := do(t, http.MethodPut, url+armBase()+"/"+svcN+"/sharedPrivateLinkResources/blob", map[string]any{
		"properties": map[string]any{"groupId": "blob", "privateLinkResourceId": "/sub/x"},
	})
	assert.Equal(t, "blob", spl["name"])

	pec := do(t, http.MethodPut, url+armBase()+"/"+svcN+"/privateEndpointConnections/pec1", map[string]any{
		"properties": map[string]any{"privateLinkServiceConnectionState": map[string]any{"status": "Approved"}},
	})
	assert.Equal(t, "pec1", pec["name"])

	assert.Len(t, do(t, http.MethodGet, url+armBase()+"/"+svcN+"/sharedPrivateLinkResources", nil)["value"], 1)
	assert.Len(t, do(t, http.MethodGet, url+armBase()+"/"+svcN+"/privateEndpointConnections", nil)["value"], 1)
}

func TestAdminKeyRegenerationPersists(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+armBase()+"/"+svcN, map[string]any{"location": "eastus"})

	orig := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/listAdminKeys", map[string]any{})
	regen := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/regenerateAdminKey/primary", map[string]any{})
	require.NotEqual(t, orig["primaryKey"], regen["primaryKey"])

	// A subsequent list must return the rotated key, not the original.
	after := do(t, http.MethodPost, url+armBase()+"/"+svcN+"/listAdminKeys", map[string]any{})
	assert.Equal(t, regen["primaryKey"], after["primaryKey"])
	assert.Equal(t, orig["secondaryKey"], after["secondaryKey"], "untouched key stays stable")
}

func TestKeyActionMethodEnforced(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+armBase()+"/"+svcN, map[string]any{"location": "eastus"})

	// listAdminKeys requires POST; a GET must not leak keys.
	req, _ := http.NewRequest(http.MethodGet, url+armBase()+"/"+svcN+"/listAdminKeys", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	// deleteQueryKey requires DELETE; a stray GET must not delete.
	req2, _ := http.NewRequest(http.MethodGet, url+armBase()+"/"+svcN+"/deleteQueryKey/somekey", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp2.StatusCode)
}

func TestServiceNotFound(t *testing.T) {
	url := newServer(t)

	req, _ := http.NewRequest(http.MethodGet, url+armBase()+"/ghost", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- real armsearch (Microsoft.Search control-plane) SDK roundtrip ---
//
// Azure ships no Go data-plane search SDK (no azsearch/azsearchindex module),
// so wire compatibility for the {service}.search.windows.net data plane is
// covered by the explicit paren-form tests in dataplane_roundtrip_test.go.

func newSearchClientFactory(t *testing.T) *armsearch.ClientFactory {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{SearchControl: cloudP.AzureSearch})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	cf, err := armsearch.NewClientFactory(sub, fakeCred{}, armClientOptions(ts))
	require.NoError(t, err)

	return cf
}

func TestSDKSearchServiceAndKeys(t *testing.T) {
	cf := newSearchClientFactory(t)
	ctx := context.Background()
	services := cf.NewServicesClient()

	poller, err := services.BeginCreateOrUpdate(ctx, rg, svcN, armsearch.Service{
		Location: to.Ptr("eastus"),
		SKU:      &armsearch.SKU{Name: to.Ptr(armsearch.SKUNameStandard)},
	}, nil, nil)
	require.NoError(t, err)

	created, err := poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, created.Name)
	assert.Equal(t, svcN, *created.Name)

	got, err := services.Get(ctx, rg, svcN, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "eastus", *got.Location)

	// Admin-key rotation must be observable via the real client.
	admin := cf.NewAdminKeysClient()

	orig, err := admin.Get(ctx, rg, svcN, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, orig.PrimaryKey)

	regen, err := admin.Regenerate(ctx, rg, svcN, armsearch.AdminKeyKindPrimary, nil, nil)
	require.NoError(t, err)
	assert.NotEqual(t, *orig.PrimaryKey, *regen.PrimaryKey)

	after, err := admin.Get(ctx, rg, svcN, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, *regen.PrimaryKey, *after.PrimaryKey, "rotation must persist")

	// Query keys create + list.
	query := cf.NewQueryKeysClient()

	qk, err := query.Create(ctx, rg, svcN, "reader", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, qk.Key)

	qpage, err := query.NewListBySearchServicePager(rg, svcN, nil, nil).NextPage(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(qpage.Value), 2)

	page, err := services.NewListByResourceGroupPager(rg, nil, nil).NextPage(ctx)
	require.NoError(t, err)
	assert.Len(t, page.Value, 1)

	_, err = services.Delete(ctx, rg, svcN, nil, nil)
	require.NoError(t, err)
}
