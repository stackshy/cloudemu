package cosmosdb_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/require"
)

// hostClient returns an azcosmos client for endpoint whose connections all
// land on the test server, the way a hosts entry or DNS override points the
// real {account}.documents.azure.com name at the emulator.
func hostClient(t *testing.T, env sqlEnv, endpoint string) *azcosmos.Client {
	t.Helper()

	base, ok := env.ts.Client().Transport.(*http.Transport)
	require.True(t, ok)

	tr := base.Clone()
	tr.TLSClientConfig.ServerName = "example.com"
	addr := env.ts.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	cred, err := azcosmos.NewKeyCredential(fakeKey)
	require.NoError(t, err)

	client, err := azcosmos.NewClientWithKey(endpoint, cred, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: &http.Client{Transport: tr},
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	require.NoError(t, err)

	return client
}

// A container created through ARM must be reachable by azcosmos on the
// account's real host, not only on the emulator's /{account}/ path endpoint.
func TestSDKARMContainerVisibleOnAccountHost(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"global host", "https://appcosmos.documents.azure.com:443/"},
		{"regional host", "https://appcosmos-eastus.documents.azure.com:443/"},
		{"path endpoint", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			env := newSQLEnv(t)
			env.createAccount(t, "rg-1", "appcosmos", "eastus")
			env.putDatabase(t, "rg-1", "appcosmos", "appdb", nil)
			env.putContainer(t, "rg-1", "appcosmos", "appdb", &armcosmos.SQLContainerResource{
				ID: to.Ptr("items"),
				PartitionKey: &armcosmos.ContainerPartitionKey{
					Paths: []*string{to.Ptr("/pk")}, Kind: to.Ptr(armcosmos.PartitionKindHash),
				},
			}, nil)

			endpoint := tc.endpoint
			if endpoint == "" {
				endpoint = env.baseU + "/appcosmos/"
			}

			cont, err := hostClient(t, env, endpoint).NewContainer("appdb", "items")
			require.NoError(t, err)

			doc, err := json.Marshal(map[string]any{"id": "i1", "pk": "a", "v": 1})
			require.NoError(t, err)

			_, err = cont.UpsertItem(ctx, azcosmos.NewPartitionKeyString("a"), doc, nil)
			require.NoError(t, err, "ARM-created container must accept data-plane writes")

			got, err := cont.ReadItem(ctx, azcosmos.NewPartitionKeyString("a"), "i1", nil)
			require.NoError(t, err)
			require.JSONEq(t, string(doc), stripSystem(t, got.Value))
		})
	}
}

func stripSystem(t *testing.T, raw []byte) string {
	t.Helper()

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	for k := range m {
		if len(k) > 0 && k[0] == '_' {
			delete(m, k)
		}
	}

	out, err := json.Marshal(m)
	require.NoError(t, err)

	return string(out)
}
