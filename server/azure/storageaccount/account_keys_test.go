package storageaccount_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createAccount is a helper that provisions an account so the key operations
// have a target.
func createAccount(t *testing.T, client *armstorage.AccountsClient, name string) {
	t.Helper()

	poller, err := client.BeginCreate(context.Background(), "rg-1", name, armstorage.AccountCreateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)},
	}, nil)
	require.NoError(t, err)

	_, err = poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
}

// TestSDKStorageAccountListKeys proves POST .../listKeys returns a two-key
// result the data plane can build a SharedKeyCredential from.
func TestSDKStorageAccountListKeys(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)
	createAccount(t, client, "acct1")

	resp, err := client.ListKeys(ctx, "rg-1", "acct1", nil)
	require.NoError(t, err)
	require.Len(t, resp.Keys, 2)

	names := map[string]string{}
	for _, k := range resp.Keys {
		require.NotNil(t, k.KeyName)
		require.NotNil(t, k.Value)
		assert.NotEmpty(t, *k.Value, "key value must not be empty")
		require.NotNil(t, k.Permissions)
		assert.Equal(t, armstorage.KeyPermissionFull, *k.Permissions)
		names[*k.KeyName] = *k.Value
	}

	assert.Contains(t, names, "key1")
	assert.Contains(t, names, "key2")

	// Keys are stable across repeated list calls.
	again, err := client.ListKeys(ctx, "rg-1", "acct1", nil)
	require.NoError(t, err)
	assert.Equal(t, *resp.Keys[0].Value, *again.Keys[0].Value)
}

// TestSDKStorageAccountRegenerateKey proves regenerateKey rotates only the named
// key and returns the full, updated list.
func TestSDKStorageAccountRegenerateKey(t *testing.T) {
	ctx := context.Background()
	client := newAccountsClient(t)
	createAccount(t, client, "acct1")

	before, err := client.ListKeys(ctx, "rg-1", "acct1", nil)
	require.NoError(t, err)

	key1Before := keyValue(before.Keys, "key1")
	key2Before := keyValue(before.Keys, "key2")

	resp, err := client.RegenerateKey(ctx, "rg-1", "acct1", armstorage.AccountRegenerateKeyParameters{
		KeyName: to.Ptr("key1"),
	}, nil)
	require.NoError(t, err)
	require.Len(t, resp.Keys, 2)

	assert.NotEqual(t, key1Before, keyValue(resp.Keys, "key1"), "key1 should have rotated")
	assert.Equal(t, key2Before, keyValue(resp.Keys, "key2"), "key2 should be unchanged")
}

// TestSDKStorageAccountListKeysMissing rejects key ops on an unknown account.
func TestSDKStorageAccountListKeysMissing(t *testing.T) {
	client := newAccountsClient(t)

	_, err := client.ListKeys(context.Background(), "rg-1", "nope", nil)
	require.Error(t, err)
}

func keyValue(keys []*armstorage.AccountKey, name string) string {
	for _, k := range keys {
		if k.KeyName != nil && *k.KeyName == name && k.Value != nil {
			return *k.Value
		}
	}

	return ""
}
