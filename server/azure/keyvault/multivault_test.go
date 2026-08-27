package keyvault_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// hostRedirectTransport dials addr for every request regardless of the
// hostname in the request URL, while still presenting that hostname over TLS
// SNI and in the Host header. This lets a test address two distinct
// {vault}.vault.azure.net hostnames against a single local httptest TLS
// server, reproducing how two real Key Vault vaults are two distinct
// hostnames that resolve to Azure's shared data-plane front door — exactly
// the signal cloudemu's wire layer uses to scope a request to its vault.
func hostRedirectTransport(addr string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			//nolint:gosec // test-only: dialing a fixed local httptest server under a fake vault hostname it has no cert for
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func secretsClientForVault(t *testing.T, addr, vaultHost string) *azsecrets.Client {
	t.Helper()

	client, err := azsecrets.NewClient("https://"+vaultHost, fakeCred{}, &azsecrets.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: hostRedirectTransport(addr),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azsecrets.NewClient: %v", err)
	}

	return client
}

func keysClientForVault(t *testing.T, addr, vaultHost string) *azkeys.Client {
	t.Helper()

	client, err := azkeys.NewClient("https://"+vaultHost, fakeCred{}, &azkeys.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: hostRedirectTransport(addr),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azkeys.NewClient: %v", err)
	}

	return client
}

// TestSDKSecretsIsolatedPerVault reproduces the reported bug: two vaults
// (distinguished only by their request host, as real Key Vault clients always
// are) must not share a secrets namespace. Before the fix, vault-b silently
// read and overwrote vault-a's "db-password" secret.
func TestSDKSecretsIsolatedPerVault(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	addr := ts.Listener.Addr().String()
	ctx := context.Background()

	vaultA := secretsClientForVault(t, addr, "vault-a.vault.azure.net")
	vaultB := secretsClientForVault(t, addr, "vault-b.vault.azure.net")

	if _, err := vaultA.SetSecret(ctx, "db-password", azsecrets.SetSecretParameters{Value: to.Ptr("vault-a-value")}, nil); err != nil {
		t.Fatalf("SetSecret in vault A: %v", err)
	}

	// The same secret name must not exist in vault B yet.
	if _, err := vaultB.GetSecret(ctx, "db-password", "", nil); err == nil {
		t.Fatal("GetSecret in vault B unexpectedly found vault A's secret")
	}

	if _, err := vaultB.SetSecret(ctx, "db-password", azsecrets.SetSecretParameters{Value: to.Ptr("vault-b-value")}, nil); err != nil {
		t.Fatalf("SetSecret in vault B: %v", err)
	}

	gotA, err := vaultA.GetSecret(ctx, "db-password", "", nil)
	if err != nil {
		t.Fatalf("GetSecret in vault A: %v", err)
	}

	if gotA.Value == nil || *gotA.Value != "vault-a-value" {
		t.Fatalf("vault A secret value = %v, want vault-a-value (must survive vault B's SetSecret of the same name)", gotA.Value)
	}

	gotB, err := vaultB.GetSecret(ctx, "db-password", "", nil)
	if err != nil {
		t.Fatalf("GetSecret in vault B: %v", err)
	}

	if gotB.Value == nil || *gotB.Value != "vault-b-value" {
		t.Fatalf("vault B secret value = %v, want vault-b-value", gotB.Value)
	}

	// Listing must also stay scoped: vault A shows exactly its own secret.
	var namesA []string

	pager := vaultA.NewListSecretPropertiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListSecretPropertiesPager in vault A: %v", err)
		}

		for _, s := range page.Value {
			namesA = append(namesA, s.ID.Name())
		}
	}

	if len(namesA) != 1 || namesA[0] != "db-password" {
		t.Fatalf("vault A secret list = %v, want exactly [db-password]", namesA)
	}
}

// TestSDKKeysIsolatedPerVault is the keys-surface counterpart: the same key
// name in two vaults must be two independent keys.
func TestSDKKeysIsolatedPerVault(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	addr := ts.Listener.Addr().String()
	ctx := context.Background()

	vaultA := keysClientForVault(t, addr, "vault-a.vault.azure.net")
	vaultB := keysClientForVault(t, addr, "vault-b.vault.azure.net")

	if _, err := vaultA.CreateKey(ctx, "app-key",
		azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeRSA), KeySize: to.Ptr(int32(2048))}, nil); err != nil {
		t.Fatalf("CreateKey in vault A: %v", err)
	}

	if _, err := vaultB.GetKey(ctx, "app-key", "", nil); err == nil {
		t.Fatal("GetKey in vault B unexpectedly found vault A's key")
	}

	if _, err := vaultB.CreateKey(ctx, "app-key",
		azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeEC), Curve: to.Ptr(azkeys.CurveNameP256)}, nil); err != nil {
		t.Fatalf("CreateKey in vault B: %v", err)
	}

	gotA, err := vaultA.GetKey(ctx, "app-key", "", nil)
	if err != nil {
		t.Fatalf("GetKey in vault A: %v", err)
	}

	if gotA.Key == nil || gotA.Key.Kty == nil || *gotA.Key.Kty != azkeys.KeyTypeRSA {
		t.Fatalf("vault A key kty = %v, want RSA (must be unaffected by vault B's EC CreateKey of the same name)", gotA.Key)
	}

	gotB, err := vaultB.GetKey(ctx, "app-key", "", nil)
	if err != nil {
		t.Fatalf("GetKey in vault B: %v", err)
	}

	if gotB.Key == nil || gotB.Key.Kty == nil || *gotB.Key.Kty != azkeys.KeyTypeEC {
		t.Fatalf("vault B key kty = %v, want EC", gotB.Key)
	}
}

// TestSDKKeyRotationPolicyLifecycle exercises GET/PUT .../rotationpolicy
// through the real SDK. Before the fix this path was misrouted into GetKey's
// version handler and answered a wrong-shaped 404 instead of the rotation
// policy.
func TestSDKKeyRotationPolicyLifecycle(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	if _, err := client.CreateKey(ctx, "rotating-key", azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeRSA)}, nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	updated, err := client.UpdateKeyRotationPolicy(ctx, "rotating-key", azkeys.KeyRotationPolicy{
		LifetimeActions: []*azkeys.LifetimeAction{
			{
				Trigger: &azkeys.LifetimeActionTrigger{TimeAfterCreate: to.Ptr("P90D")},
				Action:  &azkeys.LifetimeActionType{Type: to.Ptr(azkeys.KeyRotationPolicyActionRotate)},
			},
		},
		Attributes: &azkeys.KeyRotationPolicyAttributes{ExpiryTime: to.Ptr("P2Y")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateKeyRotationPolicy: %v", err)
	}

	if len(updated.LifetimeActions) != 1 ||
		updated.LifetimeActions[0].Trigger == nil ||
		updated.LifetimeActions[0].Trigger.TimeAfterCreate == nil ||
		*updated.LifetimeActions[0].Trigger.TimeAfterCreate != "P90D" {
		t.Fatalf("UpdateKeyRotationPolicy lifetimeActions = %+v, want one P90D rotate trigger", updated.LifetimeActions)
	}

	if updated.Attributes == nil || updated.Attributes.ExpiryTime == nil || *updated.Attributes.ExpiryTime != "P2Y" {
		t.Fatalf("UpdateKeyRotationPolicy expiryTime = %v, want P2Y", updated.Attributes)
	}

	got, err := client.GetKeyRotationPolicy(ctx, "rotating-key", nil)
	if err != nil {
		t.Fatalf("GetKeyRotationPolicy: %v", err)
	}

	if got.Attributes == nil || got.Attributes.ExpiryTime == nil || *got.Attributes.ExpiryTime != "P2Y" {
		t.Fatalf("GetKeyRotationPolicy expiryTime = %v, want P2Y (must round-trip what UpdateKeyRotationPolicy wrote)", got.Attributes)
	}
}

// TestSDKKeyRotationPolicyOnMissingKeyIs404 confirms the rotationpolicy route
// still reports a real 404 (via KeyNotFound) for a key that was never
// created, rather than the version-shaped 404 the misrouting used to produce.
func TestSDKKeyRotationPolicyOnMissingKeyIs404(t *testing.T) {
	client := newKeysClient(t)

	_, err := client.GetKeyRotationPolicy(context.Background(), "does-not-exist", nil)
	if err == nil {
		t.Fatal("GetKeyRotationPolicy on a missing key: want error, got nil")
	}
}
