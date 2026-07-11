package azuresearch_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

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
