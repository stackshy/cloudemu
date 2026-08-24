package keyvault_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newKeysClient(t *testing.T) *azkeys.Client {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := azkeys.NewClient(ts.URL, fakeCred{}, &azkeys.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azkeys.NewClient: %v", err)
	}

	return client
}

func TestSDKKeyVaultCreateRSAKeyLifecycle(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	created, err := client.CreateKey(ctx, "app-key", azkeys.CreateKeyParameters{
		Kty:     to.Ptr(azkeys.KeyTypeRSA),
		KeySize: to.Ptr(int32(2048)),
		Tags:    map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if created.Key == nil || created.Key.KID == nil || created.Key.KID.Name() != "app-key" {
		t.Fatalf("CreateKey key id = %v, want name app-key", created.Key)
	}

	if created.Key.Kty == nil || *created.Key.Kty != azkeys.KeyTypeRSA {
		t.Fatalf("CreateKey kty = %v, want RSA", created.Key.Kty)
	}

	if len(created.Key.N) == 0 || len(created.Key.E) == 0 {
		t.Fatal("CreateKey did not return RSA public material (n, e)")
	}

	got, err := client.GetKey(ctx, "app-key", "", nil)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}

	if got.Key.KID.Version() != created.Key.KID.Version() {
		t.Fatalf("GetKey version = %s, want %s", got.Key.KID.Version(), created.Key.KID.Version())
	}

	var names []string

	pager := client.NewListKeyPropertiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListKeyProperties: %v", err)
		}

		for _, item := range page.Value {
			names = append(names, item.KID.Name())
		}
	}

	if len(names) != 1 || names[0] != "app-key" {
		t.Fatalf("list = %v, want [app-key]", names)
	}

	deleted, err := client.DeleteKey(ctx, "app-key", nil)
	if err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}

	if deleted.Key.KID.Name() != "app-key" {
		t.Fatalf("DeleteKey id = %v", deleted.Key.KID)
	}

	if _, err := client.GetKey(ctx, "app-key", "", nil); err == nil {
		t.Fatal("GetKey after delete: want error, got nil")
	}
}

func TestSDKKeyVaultRSAEncryptDecrypt(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	if _, err := client.CreateKey(ctx, "enc-key", azkeys.CreateKeyParameters{
		Kty: to.Ptr(azkeys.KeyTypeRSA),
	}, nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	plaintext := []byte("attack at dawn")

	for _, alg := range []azkeys.EncryptionAlgorithm{
		azkeys.EncryptionAlgorithmRSAOAEP,
		azkeys.EncryptionAlgorithmRSAOAEP256,
		azkeys.EncryptionAlgorithmRSA15,
	} {
		enc, err := client.Encrypt(ctx, "enc-key", "", azkeys.KeyOperationParameters{
			Algorithm: to.Ptr(alg),
			Value:     plaintext,
		}, nil)
		if err != nil {
			t.Fatalf("Encrypt(%s): %v", alg, err)
		}

		dec, err := client.Decrypt(ctx, "enc-key", "", azkeys.KeyOperationParameters{
			Algorithm: to.Ptr(alg),
			Value:     enc.Result,
		}, nil)
		if err != nil {
			t.Fatalf("Decrypt(%s): %v", alg, err)
		}

		if string(dec.Result) != string(plaintext) {
			t.Fatalf("Decrypt(%s) = %q, want %q", alg, dec.Result, plaintext)
		}
	}
}

func TestSDKKeyVaultRSASignVerify(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	if _, err := client.CreateKey(ctx, "sign-key", azkeys.CreateKeyParameters{
		Kty: to.Ptr(azkeys.KeyTypeRSA),
	}, nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	digest := sha256.Sum256([]byte("message to sign"))

	for _, alg := range []azkeys.SignatureAlgorithm{
		azkeys.SignatureAlgorithmRS256,
		azkeys.SignatureAlgorithmPS256,
	} {
		sig, err := client.Sign(ctx, "sign-key", "", azkeys.SignParameters{
			Algorithm: to.Ptr(alg),
			Value:     digest[:],
		}, nil)
		if err != nil {
			t.Fatalf("Sign(%s): %v", alg, err)
		}

		ver, err := client.Verify(ctx, "sign-key", "", azkeys.VerifyParameters{
			Algorithm: to.Ptr(alg),
			Digest:    digest[:],
			Signature: sig.Result,
		}, nil)
		if err != nil {
			t.Fatalf("Verify(%s): %v", alg, err)
		}

		if ver.Value == nil || !*ver.Value {
			t.Fatalf("Verify(%s) = %v, want true", alg, ver.Value)
		}

		// A tampered digest must not verify.
		bad := sha256.Sum256([]byte("different message"))

		ver2, err := client.Verify(ctx, "sign-key", "", azkeys.VerifyParameters{
			Algorithm: to.Ptr(alg),
			Digest:    bad[:],
			Signature: sig.Result,
		}, nil)
		if err != nil {
			t.Fatalf("Verify(bad, %s): %v", alg, err)
		}

		if ver2.Value != nil && *ver2.Value {
			t.Fatalf("Verify(%s) of tampered digest = true, want false", alg)
		}
	}
}

func TestSDKKeyVaultECSignVerify(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	if _, err := client.CreateKey(ctx, "ec-key", azkeys.CreateKeyParameters{
		Kty:   to.Ptr(azkeys.KeyTypeEC),
		Curve: to.Ptr(azkeys.CurveNameP256),
	}, nil); err != nil {
		t.Fatalf("CreateKey(EC): %v", err)
	}

	digest := sha256.Sum256([]byte("ec message"))

	sig, err := client.Sign(ctx, "ec-key", "", azkeys.SignParameters{
		Algorithm: to.Ptr(azkeys.SignatureAlgorithmES256),
		Value:     digest[:],
	}, nil)
	if err != nil {
		t.Fatalf("Sign(ES256): %v", err)
	}

	ver, err := client.Verify(ctx, "ec-key", "", azkeys.VerifyParameters{
		Algorithm: to.Ptr(azkeys.SignatureAlgorithmES256),
		Digest:    digest[:],
		Signature: sig.Result,
	}, nil)
	if err != nil {
		t.Fatalf("Verify(ES256): %v", err)
	}

	if ver.Value == nil || !*ver.Value {
		t.Fatalf("Verify(ES256) = %v, want true", ver.Value)
	}
}

func TestSDKKeyVaultWrapUnwrap(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	if _, err := client.CreateKey(ctx, "wrap-key", azkeys.CreateKeyParameters{
		Kty: to.Ptr(azkeys.KeyTypeRSA),
	}, nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	cek := []byte("0123456789abcdef0123456789abcdef") // 32-byte content key

	wrapped, err := client.WrapKey(ctx, "wrap-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     cek,
	}, nil)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	unwrapped, err := client.UnwrapKey(ctx, "wrap-key", "", azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     wrapped.Result,
	}, nil)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}

	if string(unwrapped.Result) != string(cek) {
		t.Fatalf("UnwrapKey = %q, want %q", unwrapped.Result, cek)
	}
}

func TestSDKKeyVaultVersioning(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	first, err := client.CreateKey(ctx, "rot", azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeRSA)}, nil)
	if err != nil {
		t.Fatalf("CreateKey(v1): %v", err)
	}

	second, err := client.CreateKey(ctx, "rot", azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeRSA)}, nil)
	if err != nil {
		t.Fatalf("CreateKey(v2): %v", err)
	}

	if first.Key.KID.Version() == second.Key.KID.Version() {
		t.Fatal("second CreateKey reused the first version id")
	}

	var versions []string

	pager := client.NewListKeyPropertiesVersionsPager("rot", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListKeyPropertiesVersions: %v", err)
		}

		for _, item := range page.Value {
			versions = append(versions, item.KID.Version())
		}
	}

	if len(versions) != 2 {
		t.Fatalf("got %d versions %v, want 2", len(versions), versions)
	}
}

func TestSDKKeyVaultDeletedKeyRecover(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	if _, err := client.CreateKey(ctx, "recov", azkeys.CreateKeyParameters{Kty: to.Ptr(azkeys.KeyTypeRSA)}, nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if _, err := client.DeleteKey(ctx, "recov", nil); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}

	deleted, err := client.GetDeletedKey(ctx, "recov", nil)
	if err != nil {
		t.Fatalf("GetDeletedKey: %v", err)
	}

	if deleted.Key.KID.Name() != "recov" {
		t.Fatalf("GetDeletedKey name = %v", deleted.Key.KID)
	}

	if _, err := client.RecoverDeletedKey(ctx, "recov", nil); err != nil {
		t.Fatalf("RecoverDeletedKey: %v", err)
	}

	if _, err := client.GetKey(ctx, "recov", "", nil); err != nil {
		t.Fatalf("GetKey after recover: %v", err)
	}
}

func TestSDKKeyVaultKeysErrors(t *testing.T) {
	client := newKeysClient(t)
	ctx := context.Background()

	_, err := client.GetKey(ctx, "missing", "", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("GetKey(missing): got %v, want 404 ResponseError", err)
	}

	if respErr.ErrorCode != "KeyNotFound" {
		t.Fatalf("got error code %q, want KeyNotFound", respErr.ErrorCode)
	}
}
