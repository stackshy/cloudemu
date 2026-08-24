// Wire tests for the Azure subscriptions endpoints. They assert the ARM JSON
// shape a real client (az account show / az account list-locations) expects:
// a single-subscription Get, a non-empty List, and a locations list.

package subscriptions_test

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

const (
	subID    = "abc12345-0000-0000-0000-000000000000"
	tenantID = "99999999-0000-0000-0000-000000000000"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(azureserver.New(azureserver.Drivers{
		SubscriptionID: subID,
		TenantID:       tenantID,
	}))
	t.Cleanup(ts.Close)

	return ts
}

func getJSON(t *testing.T, ts *httptest.Server, path string, out any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
	require.NoError(t, err)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if len(body) > 0 && out != nil {
		require.NoError(t, json.Unmarshal(body, out), "body: %s", body)
	}

	return resp.StatusCode
}

func TestSubscriptionGet(t *testing.T) {
	ts := newServer(t)

	// A caller can ask for any subscription id; the emulator echoes it back
	// (subscription-transparent), reporting the configured tenant.
	var sub struct {
		ID             string `json:"id"`
		SubscriptionID string `json:"subscriptionId"`
		State          string `json:"state"`
		TenantID       string `json:"tenantId"`
		DisplayName    string `json:"displayName"`
	}

	status := getJSON(t, ts, "/subscriptions/"+subID+"?api-version=2022-12-01", &sub)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "/subscriptions/"+subID, sub.ID)
	assert.Equal(t, subID, sub.SubscriptionID)
	assert.Equal(t, "Enabled", sub.State)
	assert.Equal(t, tenantID, sub.TenantID)
	assert.NotEmpty(t, sub.DisplayName)
}

func TestSubscriptionList(t *testing.T) {
	ts := newServer(t)

	var out struct {
		Value []struct {
			SubscriptionID string `json:"subscriptionId"`
		} `json:"value"`
	}

	status := getJSON(t, ts, "/subscriptions?api-version=2022-12-01", &out)
	assert.Equal(t, http.StatusOK, status)
	require.Len(t, out.Value, 1)
	assert.Equal(t, subID, out.Value[0].SubscriptionID)
}

func TestSubscriptionListLocations(t *testing.T) {
	ts := newServer(t)

	var out struct {
		Value []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			ID          string `json:"id"`
			Type        string `json:"type"`
			Metadata    struct {
				RegionType string `json:"regionType"`
			} `json:"metadata"`
		} `json:"value"`
	}

	status := getJSON(t, ts, "/subscriptions/"+subID+"/locations?api-version=2022-12-01", &out)
	assert.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, out.Value)

	var found bool
	for _, loc := range out.Value {
		assert.Equal(t, "Region", loc.Type)
		assert.Equal(t, "/subscriptions/"+subID+"/locations/"+loc.Name, loc.ID)
		assert.Equal(t, "Physical", loc.Metadata.RegionType)

		if loc.Name == "eastus" {
			found = true

			assert.Equal(t, "East US", loc.DisplayName)
		}
	}

	assert.True(t, found, "eastus must be present")
}

// TestSubscriptionsDoNotShadowResourceGroups guards the routing boundary: a
// resource-group path under a subscription must not be claimed by this handler.
func TestSubscriptionsDoNotShadowResourceGroups(t *testing.T) {
	ts := newServer(t)

	// An unknown resource-group path under a subscription must reach the
	// resource-group handler (404 ResourceGroupNotFound), not be echoed back as
	// a subscription by this handler.
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	status := getJSON(t, ts, "/subscriptions/"+subID+"/resourcegroups/rg1?api-version=2021-04-01", &env)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "ResourceGroupNotFound", env.Error.Code)
}
