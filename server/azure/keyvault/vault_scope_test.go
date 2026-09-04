package keyvault_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// secretsClientAt builds an azsecrets client addressing a named vault via the
// bare-host path form (http://host/{vault}), the multi-vault addressing that
// lets a local `serve` isolate vaults without a *.vault.azure.net host.
func secretsClientAt(t *testing.T, ts *httptest.Server, vault string) *azsecrets.Client {
	t.Helper()

	client, err := azsecrets.NewClient(ts.URL+"/"+vault, fakeCred{}, &azsecrets.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azsecrets.NewClient(%s): %v", vault, err)
	}

	return client
}

// TestKeyVaultDoesNotStealBlobContainer is the B7 regression: on a bare host a
// blob container named after a Key Vault keyword (secrets/keys/certificates)
// must be created by the storage handler, not stolen (405) by Key Vault — while
// a real Key Vault op via the /{vault}/secrets form still reaches Key Vault.
func TestKeyVaultDoesNotStealBlobContainer(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		KeyVault:    cloud.KeyVault,
		BlobStorage: cloud.BlobStorage,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	for _, name := range []string{"secrets", "keys", "certificates"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, ts.URL+"/"+name+"?restype=container", nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("create container %q: %v", name, err)
		}

		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("PUT container %q = %d, want 201 (Key Vault must not steal a blob container)", name, resp.StatusCode)
		}
	}

	client := secretsClientAt(t, ts, "kv1")
	if _, err := client.SetSecret(ctx, "s1", azsecrets.SetSecretParameters{Value: to.Ptr("v1")}, nil); err != nil {
		t.Fatalf("SetSecret via /kv1/secrets should reach Key Vault: %v", err)
	}
}

// TestKeyVaultBareHostMultiVaultIsolation is the B8 regression: two vaults
// addressed by a leading path segment on a bare host are independent namespaces,
// so a secret in one is never visible through the other (previously every bare
// host collapsed into a single "default" vault).
func TestKeyVaultBareHostMultiVaultIsolation(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	vaultA := secretsClientAt(t, ts, "vault-a")
	vaultB := secretsClientAt(t, ts, "vault-b")

	if _, err := vaultA.SetSecret(ctx, "shared", azsecrets.SetSecretParameters{Value: to.Ptr("from-a")}, nil); err != nil {
		t.Fatalf("set in vault-a: %v", err)
	}

	// vault-b must NOT resolve vault-a's secret.
	if _, err := vaultB.GetSecret(ctx, "shared", "", nil); err == nil {
		t.Fatal("vault-b resolved vault-a's secret; bare-host vaults are not isolated")
	}

	// vault-b holds its own independent "shared", and it does not overwrite A's.
	if _, err := vaultB.SetSecret(ctx, "shared", azsecrets.SetSecretParameters{Value: to.Ptr("from-b")}, nil); err != nil {
		t.Fatalf("set in vault-b: %v", err)
	}

	got, err := vaultA.GetSecret(ctx, "shared", "", nil)
	if err != nil {
		t.Fatalf("get in vault-a: %v", err)
	}

	if got.Value == nil || *got.Value != "from-a" {
		t.Fatalf("vault-a secret = %v, want from-a (cross-vault contamination)", got.Value)
	}
}
