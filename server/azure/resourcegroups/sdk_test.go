// Real-SDK and wire round-trip tests for the Azure resource-group handler. The
// live azure-sdk-for-go armresources ResourceGroupsClient drives the async
// create/get/update/delete/export lifecycle end-to-end; raw HTTP pins the
// status codes and validation the SDK hides (201-on-create, 400-no-location).

package resourcegroups_test

import (
	"context"
	"encoding/json"
	"io"
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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const subID = "00000000-0000-0000-0000-000000000000"

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{SubscriptionID: subID}))
	t.Cleanup(ts.Close)

	return ts
}

func newRGClient(t *testing.T, ts *httptest.Server) *armresources.ResourceGroupsClient {
	t.Helper()

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		}},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	c, err := armresources.NewResourceGroupsClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	return c
}

// TestResourceGroupLifecycleSDK drives the full lifecycle with the real SDK,
// proving the async delete LRO, case-insensitive get, PATCH tag update and
// export all satisfy the live poller/marshalling.
func TestResourceGroupLifecycleSDK(t *testing.T) {
	ctx := context.Background()
	ts := newServer(t)
	client := newRGClient(t, ts)

	created, err := client.CreateOrUpdate(ctx, "myRG", armresources.ResourceGroup{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "eastus", *created.Location)
	assert.Equal(t, "Succeeded", *created.Properties.ProvisioningState)

	// ARM resolves the group name case-insensitively.
	got, err := client.Get(ctx, "MYRG", nil)
	require.NoError(t, err)
	assert.Equal(t, "eastus", *got.Location)
	assert.Equal(t, "prod", *got.Tags["env"])

	// PATCH merges tags and returns the group.
	updated, err := client.Update(ctx, "myrg", armresources.ResourceGroupPatchable{
		Tags: map[string]*string{"env": to.Ptr("stage"), "team": to.Ptr("core")},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "stage", *updated.Tags["env"])
	assert.Equal(t, "core", *updated.Tags["team"])

	// exportTemplate returns a valid template skeleton via the LRO poller.
	exp, err := client.BeginExportTemplate(ctx, "myRG", armresources.ExportTemplateRequest{
		Resources: []*string{to.Ptr("*")},
	}, nil)
	require.NoError(t, err)
	expRes, err := exp.PollUntilDone(ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, expRes.Template)

	// Async delete: 202 + Location the poller drives to completion.
	poller, err := client.BeginDelete(ctx, "myRG", nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	_, err = client.Get(ctx, "myRG", nil)
	require.Error(t, err, "group must be gone after delete")
}

// TestResourceGroupCreateReturns201 pins the create-vs-update status codes and
// the location-required validation the SDK abstracts away.
func TestResourceGroupCreateReturns201(t *testing.T) {
	ts := newServer(t)

	base := ts.URL + "/subscriptions/" + subID + "/resourcegroups/statusrg?api-version=2021-04-01"

	// First PUT creates -> 201.
	assert.Equal(t, http.StatusCreated, putRG(t, ts, base, `{"location":"eastus"}`))

	// Second PUT updates -> 200.
	assert.Equal(t, http.StatusOK, putRG(t, ts, base, `{"location":"eastus"}`))
}

func TestResourceGroupRequiresLocation(t *testing.T) {
	ts := newServer(t)

	base := ts.URL + "/subscriptions/" + subID + "/resourcegroups/noloc?api-version=2021-04-01"

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, base, strings.NewReader(`{}`))
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	assert.Equal(t, "LocationRequired", env.Error.Code)
}

func putRG(t *testing.T, ts *httptest.Server, url, body string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}
