// Full-server regression for the RG-existence gate on the generic-resources
// listing (az resource list). Real Azure returns 404 ResourceGroupNotFound for
// a GET scoped to a nonexistent resource group; the subscription-wide listing
// is never gated. The central RG gate cannot cover this path (no /providers/
// segment), so the guard lives in the generic-resources handler itself.

package resourcegraph_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const missingRGSub = "123456789012"

func TestGenericResourcesListMissingResourceGroup(t *testing.T) {
	srv := azureserver.NewFromProvider(cloudemu.NewAzure())
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := ts.Client()
	base := ts.URL + "/subscriptions/" + missingRGSub

	t.Run("nonexistent group returns 404 ResourceGroupNotFound", func(t *testing.T) {
		status, body := doGet(t, client, base+"/resourceGroups/ghost-rg/resources")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "ResourceGroupNotFound", errorCode(t, body))
	})

	t.Run("existing group returns 200", func(t *testing.T) {
		createRGWire(t, client, ts.URL, "real-rg")

		status, body := doGet(t, client, base+"/resourceGroups/real-rg/resources")
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, string(body), `"value"`)
	})

	t.Run("subscription-wide list is not gated", func(t *testing.T) {
		status, _ := doGet(t, client, base+"/resources")
		assert.Equal(t, http.StatusOK, status)
	})
}

func doGet(t *testing.T, client *http.Client, url string) (int, []byte) {
	t.Helper()

	resp, err := client.Get(url)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, body
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal(body, &payload))

	return payload.Error.Code
}

func createRGWire(t *testing.T, client *http.Client, baseURL, name string) {
	t.Helper()

	url := baseURL + "/subscriptions/" + missingRGSub + "/resourceGroups/" + name + "?api-version=2021-04-01"

	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(`{"location":"eastus"}`))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Less(t, resp.StatusCode, http.StatusMultipleChoices,
		"resource-group create should succeed")
}
