package keyvault_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// statusCodeOf returns the HTTP status carried by an azcore.ResponseError, or 0.
func statusCodeOf(err error) int {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return 0
	}

	return respErr.StatusCode
}

// TestSDKKeyVaultExpiredKeyBlocksEncryptAllowsDecrypt verifies that an expired
// key rejects encrypt/sign/wrapKey with 403 but still serves decrypt/verify,
// matching Azure's date-time controlled operation rules.
func TestSDKKeyVaultExpiredKeyBlocksEncryptAllowsDecrypt(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	created, err := client.CreateKey(ctx, "exp-key", azkeys.CreateKeyParameters{
		Kty: to.Ptr(azkeys.KeyTypeRSA),
	}, nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	version := created.Key.KID.Version()
	plaintext := []byte("attack at dawn")

	// Encrypt while the key is still valid, so we have ciphertext to decrypt
	// after the key expires.
	enc, err := client.Encrypt(ctx, "exp-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     plaintext,
	}, nil)
	if err != nil {
		t.Fatalf("Encrypt (valid): %v", err)
	}

	// Expire the key.
	past := time.Now().Add(-time.Hour)
	if _, err := client.UpdateKey(ctx, "exp-key", version, azkeys.UpdateKeyParameters{
		KeyAttributes: &azkeys.KeyAttributes{Expires: &past},
	}, nil); err != nil {
		t.Fatalf("UpdateKey (expire): %v", err)
	}

	// Encrypt / Sign / WrapKey must now be forbidden.
	if _, err := client.Encrypt(ctx, "exp-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     plaintext,
	}, nil); statusCodeOf(err) != http.StatusForbidden {
		t.Fatalf("Encrypt (expired): status = %d, want 403 (err=%v)", statusCodeOf(err), err)
	}

	digest := make([]byte, 32)
	if _, err := client.Sign(ctx, "exp-key", "", azkeys.SignParameters{
		Algorithm: to.Ptr(azkeys.SignatureAlgorithmRS256),
		Value:     digest,
	}, nil); statusCodeOf(err) != http.StatusForbidden {
		t.Fatalf("Sign (expired): status = %d, want 403 (err=%v)", statusCodeOf(err), err)
	}

	if _, err := client.WrapKey(ctx, "exp-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     []byte("0123456789abcdef0123456789abcdef"),
	}, nil); statusCodeOf(err) != http.StatusForbidden {
		t.Fatalf("WrapKey (expired): status = %d, want 403 (err=%v)", statusCodeOf(err), err)
	}

	// Decrypt must still succeed on the expired key.
	dec, err := client.Decrypt(ctx, "exp-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     enc.Result,
	}, nil)
	if err != nil {
		t.Fatalf("Decrypt (expired): want success, got %v", err)
	}

	if string(dec.Result) != string(plaintext) {
		t.Fatalf("Decrypt (expired) = %q, want %q", dec.Result, plaintext)
	}
}

// TestSDKKeyVaultNotYetValidKeyBlocksSign verifies a key whose nbf is in the
// future rejects sign with 403 but still serves verify.
func TestSDKKeyVaultNotYetValidKeyBlocksSign(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	future := time.Now().Add(time.Hour)
	if _, err := client.CreateKey(ctx, "nbf-key", azkeys.CreateKeyParameters{
		Kty:           to.Ptr(azkeys.KeyTypeRSA),
		KeyAttributes: &azkeys.KeyAttributes{NotBefore: &future},
	}, nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	digest := make([]byte, 32)
	if _, err := client.Sign(ctx, "nbf-key", "", azkeys.SignParameters{
		Algorithm: to.Ptr(azkeys.SignatureAlgorithmRS256),
		Value:     digest,
	}, nil); statusCodeOf(err) != http.StatusForbidden {
		t.Fatalf("Sign (not yet valid): status = %d, want 403 (err=%v)", statusCodeOf(err), err)
	}

	// Verify is not date-time controlled: it must not be blocked by the window.
	// A garbage signature simply verifies false, without a 403.
	ver, err := client.Verify(ctx, "nbf-key", "", azkeys.VerifyParameters{
		Algorithm: to.Ptr(azkeys.SignatureAlgorithmRS256),
		Digest:    digest,
		Signature: make([]byte, 256),
	}, nil)
	if err != nil {
		t.Fatalf("Verify (not yet valid): want no error, got %v", err)
	}

	if ver.Value != nil && *ver.Value {
		t.Fatal("Verify of a garbage signature returned true")
	}
}

// TestSDKKeyVaultExpiredSecretGet403 verifies GET on an expired secret is 403.
func TestSDKKeyVaultExpiredSecretGet403(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)
	if _, err := client.SetSecret(ctx, "exp-secret", azsecrets.SetSecretParameters{
		Value:            to.Ptr("hunter2"),
		SecretAttributes: &azsecrets.SecretAttributes{Expires: &past},
	}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if _, err := client.GetSecret(ctx, "exp-secret", "", nil); statusCodeOf(err) != http.StatusForbidden {
		t.Fatalf("GetSecret (expired): status = %d, want 403 (err=%v)", statusCodeOf(err), err)
	}
}

// TestSDKKeyVaultRotateKey verifies RotateKey mints a new current version while
// the previous version remains gettable.
func TestSDKKeyVaultRotateKey(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	first, err := client.CreateKey(ctx, "rot-key", azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeRSA)}, nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	firstVer := first.Key.KID.Version()

	rotated, err := client.RotateKey(ctx, "rot-key", nil)
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	newVer := rotated.Key.KID.Version()
	if newVer == "" || newVer == firstVer {
		t.Fatalf("RotateKey version = %q, want a new version (was %q)", newVer, firstVer)
	}

	// The current pointer advances to the rotated version.
	cur, err := client.GetKey(ctx, "rot-key", "", nil)
	if err != nil {
		t.Fatalf("GetKey (current): %v", err)
	}

	if cur.Key.KID.Version() != newVer {
		t.Fatalf("current version = %q, want rotated %q", cur.Key.KID.Version(), newVer)
	}

	// The original version is still retrievable.
	old, err := client.GetKey(ctx, "rot-key", firstVer, nil)
	if err != nil {
		t.Fatalf("GetKey (old version): %v", err)
	}

	if old.Key.KID.Version() != firstVer {
		t.Fatalf("old version = %q, want %q", old.Key.KID.Version(), firstVer)
	}
}

// TestKeyVaultCertificateMintsSecretAndKey verifies that creating a certificate
// also creates the addressable managed secret and key with the same name, and
// that the certificate bundle's SID/KID point at them.
func TestKeyVaultCertificateMintsSecretAndKey(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	httpClient := ts.Client()

	// Create the certificate over raw REST (no cert SDK in this module).
	status, _, raw := certRoundTrip(t, httpClient, http.MethodPost, ts.URL+"/default/certificates/tls-cert/create", selfSignedPolicy)
	if status != http.StatusAccepted {
		t.Fatalf("create cert status = %d, want 202\nbody: %s", status, raw)
	}

	// The certificate bundle advertises SID/KID pointing at the backing objects.
	status, _, raw = certRoundTrip(t, httpClient, http.MethodGet, ts.URL+"/default/certificates/tls-cert", "")
	if status != http.StatusOK {
		t.Fatalf("get cert status = %d, want 200\nbody: %s", status, raw)
	}

	bundle := decodeCert(t, raw)
	if sid, _ := bundle["sid"].(string); !strings.Contains(sid, "/secrets/tls-cert/") {
		t.Fatalf("cert bundle sid = %v, want a /secrets/tls-cert/<version> URL", bundle["sid"])
	}

	if kid, _ := bundle["kid"].(string); !strings.Contains(kid, "/keys/tls-cert/") {
		t.Fatalf("cert bundle kid = %v, want a /keys/tls-cert/<version> URL", bundle["kid"])
	}

	ctx := context.Background()

	// The addressable secret returns the certificate value and is managed.
	secrets, err := azsecrets.NewClient(ts.URL+"/default", fakeCred{}, &azsecrets.ClientOptions{
		ClientOptions:                        azcore.ClientOptions{Transport: httpClient, Retry: policy.RetryOptions{MaxRetries: -1}},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azsecrets.NewClient: %v", err)
	}

	sec, err := secrets.GetSecret(ctx, "tls-cert", "", nil)
	if err != nil {
		t.Fatalf("GetSecret(tls-cert): %v", err)
	}

	if sec.Value == nil || !strings.Contains(*sec.Value, "BEGIN CERTIFICATE") {
		t.Fatalf("managed secret value does not carry the certificate PEM: %v", sec.Value)
	}

	if sec.Managed == nil || !*sec.Managed {
		t.Fatal("managed secret Managed flag = false/nil, want true")
	}

	// The addressable key exposes the certificate's key and is managed.
	keys, err := azkeys.NewClient(ts.URL+"/default", fakeCred{}, &azkeys.ClientOptions{
		ClientOptions:                        azcore.ClientOptions{Transport: httpClient, Retry: policy.RetryOptions{MaxRetries: -1}},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azkeys.NewClient: %v", err)
	}

	key, err := keys.GetKey(ctx, "tls-cert", "", nil)
	if err != nil {
		t.Fatalf("GetKey(tls-cert): %v", err)
	}

	if key.Key == nil || key.Key.Kty == nil || *key.Key.Kty != azkeys.KeyTypeRSA {
		t.Fatalf("managed key kty = %v, want RSA", key.Key)
	}

	if key.Managed == nil || !*key.Managed {
		t.Fatal("managed key Managed flag = false/nil, want true")
	}
}
