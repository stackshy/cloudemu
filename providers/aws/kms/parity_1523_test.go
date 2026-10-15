package kms_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func wantException(t *testing.T, err error, exception string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != exception {
		t.Fatalf("err = %v, want %s", err, exception)
	}
}

// --- DeriveSharedSecret (ECDH) ---

func TestDeriveSharedSecretMatchesLocalECDH(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for _, tc := range []struct {
		spec  string
		curve elliptic.Curve
	}{
		{driver.SpecECCNISTP256, elliptic.P256()},
		{driver.SpecECCNISTP384, elliptic.P384()},
		{driver.SpecECCNISTP521, elliptic.P521()},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			k, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: tc.spec, KeyUsage: driver.UsageKeyAgreement})
			if err != nil {
				t.Fatalf("CreateKey KEY_AGREEMENT: %v", err)
			}

			if len(k.KeyAgreementAlgorithms) != 1 || k.KeyAgreementAlgorithms[0] != driver.KeyAgreementECDH {
				t.Fatalf("KeyAgreementAlgorithms = %v, want [ECDH]", k.KeyAgreementAlgorithms)
			}

			if len(k.SigningAlgorithms) != 0 || len(k.EncryptionAlgorithms) != 0 {
				t.Fatalf("unexpected algorithms on KEY_AGREEMENT key: %+v", k)
			}

			// Bob's key pair lives outside KMS.
			bob, err := ecdsa.GenerateKey(tc.curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}

			bobPub, err := x509.MarshalPKIXPublicKey(&bob.PublicKey)
			if err != nil {
				t.Fatal(err)
			}

			out, err := m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
				KeyID: k.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: bobPub,
			})
			if err != nil {
				t.Fatalf("DeriveSharedSecret: %v", err)
			}

			if out.KeyAgreementAlgorithm != driver.KeyAgreementECDH || out.KeyOrigin != driver.OriginAWSKMS {
				t.Fatalf("output = %+v", out)
			}

			// Bob derives the same secret from Alice's (KMS) public key.
			pk, err := m.GetPublicKey(ctx, driver.GetPublicKeyInput{KeyID: k.KeyID})
			if err != nil {
				t.Fatalf("GetPublicKey: %v", err)
			}

			if len(pk.KeyAgreementAlgorithms) != 1 {
				t.Fatalf("GetPublicKey KeyAgreementAlgorithms = %v", pk.KeyAgreementAlgorithms)
			}

			parsed, err := x509.ParsePKIXPublicKey(pk.PublicKey)
			if err != nil {
				t.Fatal(err)
			}

			alicePub, err := parsed.(*ecdsa.PublicKey).ECDH()
			if err != nil {
				t.Fatal(err)
			}

			bobECDH, err := bob.ECDH()
			if err != nil {
				t.Fatal(err)
			}

			want, err := bobECDH.ECDH(alicePub)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(out.SharedSecret, want) {
				t.Fatalf("shared secret mismatch:\n kms   %x\n local %x", out.SharedSecret, want)
			}
		})
	}
}

func TestDeriveSharedSecretErrors(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	agree, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageKeyAgreement})
	if err != nil {
		t.Fatal(err)
	}

	signKey, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageSignVerify})
	if err != nil {
		t.Fatal(err)
	}

	peer256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	peerDER, _ := x509.MarshalPKIXPublicKey(&peer256.PublicKey)

	peer384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	peer384DER, _ := x509.MarshalPKIXPublicKey(&peer384.PublicKey)

	// SIGN_VERIFY key → InvalidKeyUsageException.
	_, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: signKey.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if !errors.Is(err, driver.ErrInvalidKeyUsage) {
		t.Fatalf("sign key: err = %v, want ErrInvalidKeyUsage", err)
	}

	// Peer on a different curve → validation error.
	_, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peer384DER,
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("curve mismatch: err = %v, want InvalidArgument", err)
	}

	// Garbage public key / bad algorithm → validation error.
	if _, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: []byte("nope"),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad DER: err = %v", err)
	}

	if _, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: "XDH", PublicKey: peerDER,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad algorithm: err = %v", err)
	}

	// Disabled key → DisabledException.
	if err := m.DisableKey(ctx, agree.KeyID); err != nil {
		t.Fatal(err)
	}

	_, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if !errors.Is(err, driver.ErrKeyDisabled) {
		t.Fatalf("disabled: err = %v", err)
	}

	// A KEY_AGREEMENT key can't sign.
	if err := m.EnableKey(ctx, agree.KeyID); err != nil {
		t.Fatal(err)
	}

	_, err = m.Sign(ctx, driver.SignInput{
		KeyID: agree.KeyID, Message: []byte("x"), MessageType: driver.MessageTypeRaw,
		SigningAlgorithm: driver.SignECDSASHA256,
	})
	if !errors.Is(err, driver.ErrInvalidKeyUsage) {
		t.Fatalf("sign with KEY_AGREEMENT key: err = %v", err)
	}
}

func TestCreateKeyKeyAgreementSpecRules(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for _, spec := range []string{driver.SpecSymmetricDefault, driver.SpecRSA2048, driver.SpecHMAC256} {
		if _, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: spec, KeyUsage: driver.UsageKeyAgreement}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("%s + KEY_AGREEMENT: err = %v, want InvalidArgument", spec, err)
		}
	}
}

func TestKeyAgreementKeySurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	k, err := src.CreateKey(ctx, driver.CreateKeyInput{KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageKeyAgreement})
	if err != nil {
		t.Fatal(err)
	}

	peer, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	peerDER, _ := x509.MarshalPKIXPublicKey(&peer.PublicKey)

	before, err := src.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: k.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	after, err := dst.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: k.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before.SharedSecret, after.SharedSecret) {
		t.Fatal("shared secret changed across snapshot/restore")
	}
}

// --- reserved aws: tag prefix ---

func TestReservedAWSTagPrefixRejected(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	_, err := m.CreateKey(ctx, driver.CreateKeyInput{Tags: map[string]string{"aws:cloudformation:stack-name": "x"}})
	wantException(t, err, driver.ExTag)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Tags: map[string]string{"team": "a", "awsome": "ok"}})
	if err != nil {
		t.Fatalf("CreateKey with normal tags: %v", err)
	}

	for _, key := range []string{"aws:foo", "AWS:Foo", "Aws:bar"} {
		err := m.TagResource(ctx, k.KeyID, map[string]string{"ok": "1", key: "v"})
		wantException(t, err, driver.ExTag)
	}

	tags, err := m.ListResourceTags(ctx, k.KeyID)
	if err != nil {
		t.Fatal(err)
	}

	if len(tags) != 2 || tags["ok"] != "" {
		t.Fatalf("rejected TagResource must not partially apply; tags = %v", tags)
	}

	if err := m.TagResource(ctx, k.KeyID, map[string]string{"owner": "me"}); err != nil {
		t.Fatalf("TagResource normal: %v", err)
	}
}

// --- CreateGrant principal validation ---

func TestCreateGrantPrincipalValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}

	ops := []string{driver.GrantOpEncrypt}

	valid := []string{
		"arn:aws:iam::111122223333:root",
		"arn:aws:iam::111122223333:user/alice",
		"arn:aws:iam::111122223333:role/ExampleRole",
		"arn:aws:sts::111122223333:assumed-role/ExampleRole/session",
		"arn:aws:sts::111122223333:federated-user/bob",
		"arn:aws-us-gov:iam::111122223333:role/r",
		"dynamodb.us-east-1.amazonaws.com",
		"111122223333",
	}
	for _, p := range valid {
		if _, _, err := m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: p, Operations: ops}); err != nil {
			t.Errorf("GranteePrincipal %q rejected: %v", p, err)
		}
	}

	for _, p := range []string{"not-an-arn", "alice", "arn:aws:s3:::bucket", "arn:aws:iam", "arn:aws:iam:::role/x"} {
		_, _, err := m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: p, Operations: ops})
		wantException(t, err, driver.ExInvalidArn)
	}

	// Outside the modeled pattern → ValidationException (no exception tag).
	_, _, err = m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: "has space", Operations: ops})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("pattern violation: err = %v", err)
	}

	var apiErr *driver.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("pattern violation should be a plain validation error, got %s", apiErr.Exception)
	}

	// RetiringPrincipal is validated the same way.
	_, _, err = m.CreateGrant(ctx, driver.CreateGrantInput{
		KeyID: k.KeyID, GranteePrincipal: valid[0], RetiringPrincipal: "bogus", Operations: ops,
	})
	wantException(t, err, driver.ExInvalidArn)
}

// --- custom key stores ---

const (
	testCluster = "cluster-abcdefghijk"
	testTA      = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	testXksAKID = "ABCDEFGHIJKLMNOPQRST"
	testXksSK   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ="
)

func newHSMStore(t *testing.T, m *kms.Mock, name, cluster string) string {
	t.Helper()

	id, err := m.CreateCustomKeyStore(context.Background(), driver.CreateCustomKeyStoreInput{
		CustomKeyStoreName: name, CloudHsmClusterID: cluster, TrustAnchorCertificate: testTA, KeyStorePassword: "kmsPswd1",
	})
	if err != nil {
		t.Fatalf("CreateCustomKeyStore: %v", err)
	}

	return id
}

func xksInput(name, endpoint, path string) driver.CreateCustomKeyStoreInput {
	return driver.CreateCustomKeyStoreInput{
		CustomKeyStoreName:   name,
		CustomKeyStoreType:   driver.CustomKeyStoreTypeExternal,
		XksProxyURIEndpoint:  endpoint,
		XksProxyURIPath:      path,
		XksProxyConnectivity: driver.XksConnectivityPublicEndpoint,
		XksProxyAuthenticationCredential: &driver.XksProxyAuthenticationCredential{
			AccessKeyID: testXksAKID, RawSecretAccessKey: testXksSK,
		},
	}
}

func describeOne(t *testing.T, m *kms.Mock, id string) driver.CustomKeyStore {
	t.Helper()

	out, err := m.DescribeCustomKeyStores(context.Background(), id, "")
	if err != nil || len(out) != 1 {
		t.Fatalf("DescribeCustomKeyStores(%s) = %v, %v", id, out, err)
	}

	return out[0]
}

func TestCustomKeyStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	id := newHSMStore(t, m, "hsm-store", testCluster)

	s := describeOne(t, m, id)
	if s.ConnectionState != driver.ConnectionStateDisconnected || s.CustomKeyStoreType != driver.CustomKeyStoreTypeCloudHSM ||
		s.CloudHsmClusterID != testCluster || s.TrustAnchorCertificate != testTA {
		t.Fatalf("new store = %+v", s)
	}

	// Duplicate name / cluster.
	_, err := m.CreateCustomKeyStore(ctx, driver.CreateCustomKeyStoreInput{
		CustomKeyStoreName: "hsm-store", CloudHsmClusterID: "cluster-zzzzzzzzzzz", TrustAnchorCertificate: testTA, KeyStorePassword: "kmsPswd1",
	})
	wantException(t, err, driver.ExCustomKeyStoreNameInUse)

	_, err = m.CreateCustomKeyStore(ctx, driver.CreateCustomKeyStoreInput{
		CustomKeyStoreName: "other", CloudHsmClusterID: testCluster, TrustAnchorCertificate: testTA, KeyStorePassword: "kmsPswd1",
	})
	wantException(t, err, driver.ExCloudHsmClusterInUse)

	// Missing required CloudHSM params → validation.
	if _, err = m.CreateCustomKeyStore(ctx, driver.CreateCustomKeyStoreInput{CustomKeyStoreName: "x"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing params: %v", err)
	}

	// Creating a key in a DISCONNECTED store fails.
	_, err = m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id})
	wantException(t, err, driver.ExCustomKeyStoreInvalidState)

	// Disconnecting a DISCONNECTED store fails; connecting succeeds.
	wantException(t, m.DisconnectCustomKeyStore(ctx, id), driver.ExCustomKeyStoreInvalidState)

	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if st := describeOne(t, m, id).ConnectionState; st != driver.ConnectionStateConnected {
		t.Fatalf("state after connect = %s", st)
	}

	// Rename allowed while CONNECTED; password change is not.
	if err := m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{CustomKeyStoreID: id, NewCustomKeyStoreName: "renamed"}); err != nil {
		t.Fatalf("rename while connected: %v", err)
	}

	err = m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{CustomKeyStoreID: id, KeyStorePassword: "newPassw0rd"})
	wantException(t, err, driver.ExCustomKeyStoreInvalidState)

	// Delete while CONNECTED → invalid state.
	wantException(t, m.DeleteCustomKeyStore(ctx, id), driver.ExCustomKeyStoreInvalidState)

	// Create a key in the connected store; it works for crypto.
	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id})
	if err != nil {
		t.Fatalf("CreateKey in store: %v", err)
	}

	if k.CustomKeyStoreID != id || k.CloudHsmClusterID != testCluster || k.Origin != driver.OriginAWSCloudHSM {
		t.Fatalf("key metadata = %+v", k)
	}

	if _, err := m.Encrypt(ctx, driver.EncryptInput{KeyID: k.KeyID, Plaintext: []byte("hi")}); err != nil {
		t.Fatalf("Encrypt in connected store: %v", err)
	}

	// Only symmetric keys may live in a store.
	_, err = m.CreateKey(ctx, driver.CreateKeyInput{
		Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id, KeySpec: driver.SpecRSA2048, KeyUsage: driver.UsageSignVerify,
	})
	if !errors.Is(err, driver.ErrUnsupportedOperation) {
		t.Fatalf("asymmetric in store: %v", err)
	}

	// Rotation is unsupported for custom key store keys.
	if err := m.EnableKeyRotation(ctx, k.KeyID, 0); !errors.Is(err, driver.ErrUnsupportedOperation) {
		t.Fatalf("EnableKeyRotation on CKS key: %v", err)
	}

	// Disconnect: crypto on its keys now fails; update password works.
	if err := m.DisconnectCustomKeyStore(ctx, id); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if _, err := m.Encrypt(ctx, driver.EncryptInput{KeyID: k.KeyID, Plaintext: []byte("hi")}); !errors.Is(err, driver.ErrKeyInvalidState) {
		t.Fatalf("Encrypt with disconnected store: %v", err)
	}

	if err := m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{CustomKeyStoreID: id, KeyStorePassword: "newPassw0rd"}); err != nil {
		t.Fatalf("password update while disconnected: %v", err)
	}

	// Store still holds a key → CustomKeyStoreHasCMKsException.
	wantException(t, m.DeleteCustomKeyStore(ctx, id), driver.ExCustomKeyStoreHasCMKs)

	// Schedule deletion; once the waiting period passes the store can go.
	if _, err := m.ScheduleKeyDeletion(ctx, k.KeyID, 7); err != nil {
		t.Fatal(err)
	}

	wantException(t, m.DeleteCustomKeyStore(ctx, id), driver.ExCustomKeyStoreHasCMKs)
}

func TestCustomKeyStoreDeleteAfterKeyDeletion(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Unix(0, 0))
	m := kms.New(config.NewOptions(config.WithClock(clock), config.WithRegion("us-east-1"), config.WithAccountID("000000000000")))

	id := newHSMStore(t, m, "s", testCluster)
	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.ScheduleKeyDeletion(ctx, k.KeyID, 7); err != nil {
		t.Fatal(err)
	}

	if err := m.DisconnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	wantException(t, m.DeleteCustomKeyStore(ctx, id), driver.ExCustomKeyStoreHasCMKs)

	clock.Advance(8 * 24 * time.Hour)

	if err := m.DeleteCustomKeyStore(ctx, id); err != nil {
		t.Fatalf("Delete after key deletion window: %v", err)
	}

	_, err = m.DescribeCustomKeyStores(ctx, id, "")
	wantException(t, err, driver.ExCustomKeyStoreNotFound)

	wantException(t, m.ConnectCustomKeyStore(ctx, id), driver.ExCustomKeyStoreNotFound)
}

func TestExternalKeyStore(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	id, err := m.CreateCustomKeyStore(ctx, xksInput("xks", "https://xks.example.com", "/kms/xks/v1"))
	if err != nil {
		t.Fatalf("CreateCustomKeyStore XKS: %v", err)
	}

	s := describeOne(t, m, id)
	if s.XksProxyConfiguration == nil || s.XksProxyConfiguration.AccessKeyID != testXksAKID ||
		s.XksProxyConfiguration.URIEndpoint != "https://xks.example.com" {
		t.Fatalf("xks store = %+v", s)
	}

	// Same endpoint+path → XksProxyUriInUseException.
	_, err = m.CreateCustomKeyStore(ctx, xksInput("xks2", "https://xks.example.com", "/kms/xks/v1"))
	wantException(t, err, driver.ExXksProxyURIInUse)

	// Bad path / endpoint → validation.
	if _, err = m.CreateCustomKeyStore(ctx, xksInput("xks3", "http://xks.example.com", "/kms/xks/v1")); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("http endpoint: %v", err)
	}

	if _, err = m.CreateCustomKeyStore(ctx, xksInput("xks3", "https://xks.example.com", "/bad")); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad path: %v", err)
	}

	// VPC connectivity needs an endpoint service name.
	vin := xksInput("vpc", "https://vpc.example.com", "/kms/xks/v1")
	vin.XksProxyConnectivity = driver.XksConnectivityVpcEndpointService

	if _, err = m.CreateCustomKeyStore(ctx, vin); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("vpc without service name: %v", err)
	}

	vin.XksProxyVpcEndpointServiceName = "com.amazonaws.vpce.us-east-1.vpce-svc-0123456789abcdef0"
	if _, err = m.CreateCustomKeyStore(ctx, vin); err != nil {
		t.Fatalf("vpc store: %v", err)
	}

	// A public store can't share the VPC store's endpoint.
	_, err = m.CreateCustomKeyStore(ctx, xksInput("pub", "https://vpc.example.com", "/p/kms/xks/v1"))
	wantException(t, err, driver.ExXksProxyURIEndpointInUse)

	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	// XksKeyId required, unique within the store.
	_, err = m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginExternalKeyStore, CustomKeyStoreID: id})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing XksKeyId: %v", err)
	}

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginExternalKeyStore, CustomKeyStoreID: id, XksKeyID: "ext-key-1"})
	if err != nil {
		t.Fatalf("CreateKey XKS: %v", err)
	}

	if k.XksKeyID != "ext-key-1" || k.CustomKeyStoreID != id {
		t.Fatalf("xks key = %+v", k)
	}

	_, err = m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginExternalKeyStore, CustomKeyStoreID: id, XksKeyID: "ext-key-1"})
	wantException(t, err, driver.ExXksKeyAlreadyInUse)

	// Wrong origin for the store type.
	_, err = m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("origin mismatch: %v", err)
	}

	// While CONNECTED: path + credential updatable, endpoint is not.
	if err := m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, XksProxyURIPath: "/v2pre/kms/xks/v1",
	}); err != nil {
		t.Fatalf("update path while connected: %v", err)
	}

	err = m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, XksProxyURIEndpoint: "https://new.example.com",
	})
	wantException(t, err, driver.ExCustomKeyStoreInvalidState)

	// CloudHSM-only params rejected for an XKS store.
	if err := m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, KeyStorePassword: "abcdefgh",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("password on xks: %v", err)
	}

	// Describe by name, and both id+name → validation.
	byName, err := m.DescribeCustomKeyStores(ctx, "", "xks")
	if err != nil || len(byName) != 1 || byName[0].CustomKeyStoreID != id {
		t.Fatalf("describe by name = %v, %v", byName, err)
	}

	if _, err := m.DescribeCustomKeyStores(ctx, id, "xks"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("id+name: %v", err)
	}

	_, err = m.DescribeCustomKeyStores(ctx, "", "missing")
	wantException(t, err, driver.ExCustomKeyStoreNotFound)
}

func TestCustomKeyStoreSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	id := newHSMStore(t, src, "persisted", testCluster)
	if err := src.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	k, err := src.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	got := describeOne(t, dst, id)
	if got.CustomKeyStoreName != "persisted" || got.ConnectionState != driver.ConnectionStateConnected {
		t.Fatalf("restored store = %+v", got)
	}

	md, err := dst.DescribeKey(ctx, k.KeyID)
	if err != nil || md.CustomKeyStoreID != id {
		t.Fatalf("restored key = %+v, %v", md, err)
	}

	// Name uniqueness still enforced after restore.
	_, err = dst.CreateCustomKeyStore(ctx, driver.CreateCustomKeyStoreInput{
		CustomKeyStoreName: "persisted", CloudHsmClusterID: "cluster-zzzzzzzzzzz", TrustAnchorCertificate: testTA, KeyStorePassword: "kmsPswd1",
	})
	wantException(t, err, driver.ExCustomKeyStoreNameInUse)
}
