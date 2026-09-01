// Real-SDK round-trip tests for the Azure resource-providers endpoints. The
// live azure-sdk-for-go armresources ProvidersClient drives List, Get, Register
// and Unregister end-to-end, proving the wire shapes satisfy the SDK's
// marshalling and that register/unregister flip the reported RegistrationState.

package providers_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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

func newClient(t *testing.T) *armresources.ProvidersClient {
	t.Helper()

	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{SubscriptionID: subID}))
	t.Cleanup(ts.Close)

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		}},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	c, err := armresources.NewProvidersClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	return c
}

func TestProvidersListSDK(t *testing.T) {
	ctx := context.Background()
	client := newClient(t)

	pager := client.NewListPager(nil)

	var got []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, p := range page.Value {
			require.NotNil(t, p.Namespace)
			require.NotNil(t, p.RegistrationState)
			got = append(got, *p.Namespace)
		}
	}

	assert.Contains(t, got, "Microsoft.Compute")
	assert.Contains(t, got, "Microsoft.KeyVault")
}

func TestProviderGetSDK(t *testing.T) {
	ctx := context.Background()
	client := newClient(t)

	resp, err := client.Get(ctx, "Microsoft.Storage", nil)
	require.NoError(t, err)

	require.NotNil(t, resp.Namespace)
	assert.Equal(t, "Microsoft.Storage", *resp.Namespace)
	require.NotEmpty(t, resp.ResourceTypes)
	assert.Equal(t, "storageAccounts", *resp.ResourceTypes[0].ResourceType)
	require.NotNil(t, resp.RegistrationState)
	assert.Equal(t, "Registered", *resp.RegistrationState)
}

func TestProviderGetIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	client := newClient(t)

	resp, err := client.Get(ctx, "microsoft.keyvault", nil)
	require.NoError(t, err)

	require.NotNil(t, resp.Namespace)
	assert.Equal(t, "Microsoft.KeyVault", *resp.Namespace)
}

func TestProviderRegisterUnregisterSDK(t *testing.T) {
	ctx := context.Background()
	client := newClient(t)

	// Seeded NotRegistered.
	got, err := client.Get(ctx, "Microsoft.KeyVault", nil)
	require.NoError(t, err)
	require.NotNil(t, got.RegistrationState)
	assert.Equal(t, "NotRegistered", *got.RegistrationState)

	reg, err := client.Register(ctx, "Microsoft.KeyVault", nil)
	require.NoError(t, err)
	require.NotNil(t, reg.RegistrationState)
	assert.Equal(t, "Registered", *reg.RegistrationState)

	// Persisted across a fresh Get on the same server.
	after, err := client.Get(ctx, "Microsoft.KeyVault", nil)
	require.NoError(t, err)
	assert.Equal(t, "Registered", *after.RegistrationState)

	unreg, err := client.Unregister(ctx, "Microsoft.KeyVault", nil)
	require.NoError(t, err)
	require.NotNil(t, unreg.RegistrationState)
	assert.Equal(t, "NotRegistered", *unreg.RegistrationState)
}

func TestProviderGetUnknownNamespace(t *testing.T) {
	ctx := context.Background()
	client := newClient(t)

	_, err := client.Get(ctx, "Microsoft.DoesNotExist", nil)
	require.Error(t, err)
}
