// Wire test for the global tenants list. It pins that GET /tenants returns the
// ARM JSON shape (not the blob-storage XML fallback that used to answer any
// non-/subscriptions path), which is what an account-connect flow expects.

package tenants_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const tenantID = "77777777-0000-0000-0000-000000000000"

func TestTenantsList(t *testing.T) {
	ts := httptest.NewServer(azureserver.New(azureserver.Drivers{TenantID: tenantID}))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		ts.URL+"/tenants?api-version=2022-12-01", nil)
	require.NoError(t, err)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var out struct {
		Value []struct {
			ID             string `json:"id"`
			TenantID       string `json:"tenantId"`
			TenantCategory string `json:"tenantCategory"`
		} `json:"value"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "body: %s", body)

	require.Len(t, out.Value, 1)
	assert.Equal(t, tenantID, out.Value[0].TenantID)
	assert.Equal(t, "/tenants/"+tenantID, out.Value[0].ID)
	assert.Equal(t, "Home", out.Value[0].TenantCategory)
}
