package kms_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

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

func wantKeyState(t *testing.T, m *kms.Mock, keyID, state string, enabled bool) {
	t.Helper()

	md, err := m.DescribeKey(context.Background(), keyID)
	if err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}

	if md.KeyState != state || md.Enabled != enabled {
		t.Fatalf("DescribeKey state = %s enabled=%v, want %s enabled=%v", md.KeyState, md.Enabled, state, enabled)
	}

	keys, err := m.ListKeys(context.Background())
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	for i := range keys {
		if keys[i].KeyID == keyID && keys[i].KeyState != state {
			t.Fatalf("ListKeys state = %s, want %s", keys[i].KeyState, state)
		}
	}
}

func wantUnavailable(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, driver.ErrKeyInvalidState) || errors.Is(err, driver.ErrKeyDisabled) {
		t.Fatalf("err = %v, want ErrKeyInvalidState", err)
	}

	if msg := cerrors.Message(err); !strings.HasSuffix(msg, " is unavailable") {
		t.Fatalf("message = %q, want '<key ARN> is unavailable'", msg)
	}
}

// TestCustomKeyStoreKeyUnavailableWhileDisconnected: a key in a store that is
// not CONNECTED reports KeyState Unavailable (except PendingDeletion), crypto
// fails with KMSInvalidStateException even for a Disabled key, and
// EnableKey/DisableKey take effect once the store reconnects.
func TestCustomKeyStoreKeyUnavailableWhileDisconnected(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	id := newHSMStore(t, m, "hsm", testCluster)
	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id})
	if err != nil {
		t.Fatal(err)
	}

	wantKeyState(t, m, k.KeyID, driver.StateEnabled, true)

	if err := m.DisconnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	wantKeyState(t, m, k.KeyID, driver.StateUnavailable, false)

	_, err = m.Encrypt(ctx, driver.EncryptInput{KeyID: k.KeyID, Plaintext: []byte("hi")})
	wantUnavailable(t, err)

	// DisableKey succeeds, but the key stays Unavailable and crypto still fails
	// with KMSInvalidStateException rather than DisabledException.
	if err := m.DisableKey(ctx, k.KeyID); err != nil {
		t.Fatalf("DisableKey on unavailable key: %v", err)
	}

	wantKeyState(t, m, k.KeyID, driver.StateUnavailable, false)

	_, err = m.GenerateDataKey(ctx, driver.GenerateDataKeyInput{KeyID: k.KeyID, KeySpec: "AES_256"})
	wantUnavailable(t, err)

	// A PendingDeletion key keeps that state while its store is disconnected,
	// and canceling the deletion makes it Unavailable again.
	if _, err := m.ScheduleKeyDeletion(ctx, k.KeyID, 7); err != nil {
		t.Fatalf("ScheduleKeyDeletion on unavailable key: %v", err)
	}

	wantKeyState(t, m, k.KeyID, driver.StatePendingDeletion, false)

	canceled, err := m.CancelKeyDeletion(ctx, k.KeyID)
	if err != nil || canceled.KeyState != driver.StateUnavailable {
		t.Fatalf("CancelKeyDeletion = %+v, %v", canceled, err)
	}

	// Reconnecting reveals the stored state: Disabled.
	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	wantKeyState(t, m, k.KeyID, driver.StateDisabled, false)

	if _, err := m.Encrypt(ctx, driver.EncryptInput{KeyID: k.KeyID, Plaintext: []byte("hi")}); !errors.Is(err, driver.ErrKeyDisabled) {
		t.Fatalf("Encrypt on disabled key after reconnect: %v", err)
	}

	if err := m.EnableKey(ctx, k.KeyID); err != nil {
		t.Fatal(err)
	}

	wantKeyState(t, m, k.KeyID, driver.StateEnabled, true)

	// The derived state is never persisted: a snapshot taken while the store
	// is disconnected restores the stored Enabled state.
	if err := m.DisconnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	wantKeyState(t, dst, k.KeyID, driver.StateUnavailable, false)

	if err := dst.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	wantKeyState(t, dst, k.KeyID, driver.StateEnabled, true)
}

// TestCustomKeyStoreCreateKeyUnsupportedMessage: a key that can't live in a
// custom key store fails with UnsupportedOperationException naming the rule.
func TestCustomKeyStoreCreateKeyUnsupportedMessage(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	id := newHSMStore(t, m, "hsm", testCluster)
	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	for name, in := range map[string]driver.CreateKeyInput{
		"multi-region": {Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id, MultiRegion: true},
		"rsa":          {Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id, KeySpec: driver.SpecRSA2048, KeyUsage: driver.UsageSignVerify},
		"hmac":         {Origin: driver.OriginAWSCloudHSM, CustomKeyStoreID: id, KeySpec: driver.SpecHMAC256, KeyUsage: driver.UsageGenerateVerifyMac},
	} {
		_, err := m.CreateKey(ctx, in)
		if !errors.Is(err, driver.ErrUnsupportedOperation) {
			t.Fatalf("%s: err = %v, want ErrUnsupportedOperation", name, err)
		}

		if msg := cerrors.Message(err); !strings.Contains(msg, "only single-Region symmetric encryption keys") {
			t.Fatalf("%s: message = %q", name, msg)
		}
	}
}

// TestGenerateRandomInCustomKeyStore: GenerateRandom with a CustomKeyStoreId
// needs an existing, CONNECTED CloudHSM store.
func TestGenerateRandomInCustomKeyStore(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	_, err := m.GenerateRandomInCustomKeyStore(ctx, "cks-missing", 16)
	wantException(t, err, driver.ExCustomKeyStoreNotFound)

	hsm := newHSMStore(t, m, "hsm", testCluster)

	_, err = m.GenerateRandomInCustomKeyStore(ctx, hsm, 16)
	wantException(t, err, driver.ExCustomKeyStoreInvalidState)

	if _, err := m.GenerateRandomInCustomKeyStore(ctx, hsm, 0); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("NumberOfBytes 0: %v", err)
	}

	if err := m.ConnectCustomKeyStore(ctx, hsm); err != nil {
		t.Fatal(err)
	}

	out, err := m.GenerateRandomInCustomKeyStore(ctx, hsm, 16)
	if err != nil || len(out) != 16 {
		t.Fatalf("connected store = %d bytes, %v", len(out), err)
	}

	xks, err := m.CreateCustomKeyStore(ctx, xksInput("xks", "https://xks.example.com", "/kms/xks/v1"))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.ConnectCustomKeyStore(ctx, xks); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GenerateRandomInCustomKeyStore(ctx, xks, 16); !errors.Is(err, driver.ErrUnsupportedOperation) {
		t.Fatalf("external key store: %v", err)
	}
}

// TestUpdateExternalKeyStoreConnectivity switches an external key store
// between PUBLIC_ENDPOINT and VPC_ENDPOINT_SERVICE.
func TestUpdateExternalKeyStoreConnectivity(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	const svc = "com.amazonaws.vpce.us-east-1.vpce-svc-0123456789abcdef0"

	id, err := m.CreateCustomKeyStore(ctx, xksInput("xks", "https://xks.example.com", "/kms/xks/v1"))
	if err != nil {
		t.Fatal(err)
	}

	// To VPC_ENDPOINT_SERVICE: needs a new endpoint and a service name.
	for _, in := range []driver.UpdateCustomKeyStoreInput{
		{CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityVpcEndpointService},
		{CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityVpcEndpointService, XksProxyURIEndpoint: "https://vpc.example.com"},
		{CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityVpcEndpointService, XksProxyVpcEndpointServiceName: svc},
	} {
		if err := m.UpdateCustomKeyStore(ctx, in); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("update %+v: err = %v, want validation error", in, err)
		}
	}

	if err := m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityVpcEndpointService,
		XksProxyURIEndpoint: "https://vpc.example.com", XksProxyVpcEndpointServiceName: svc,
		XksProxyVpcEndpointServiceOwner: "111122223333",
	}); err != nil {
		t.Fatalf("switch to VPC_ENDPOINT_SERVICE: %v", err)
	}

	cfg := describeOne(t, m, id).XksProxyConfiguration
	if cfg.Connectivity != driver.XksConnectivityVpcEndpointService || cfg.URIEndpoint != "https://vpc.example.com" ||
		cfg.VpcEndpointServiceName != svc || cfg.VpcEndpointServiceOwner != "111122223333" {
		t.Fatalf("after switch to VPC = %+v", cfg)
	}

	// Connectivity can only change while DISCONNECTED.
	if err := m.ConnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	err = m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityPublicEndpoint, XksProxyURIEndpoint: "https://pub.example.com",
	})
	wantException(t, err, driver.ExCustomKeyStoreInvalidState)

	if err := m.DisconnectCustomKeyStore(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Back to PUBLIC_ENDPOINT: needs a new endpoint and drops the service.
	err = m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityPublicEndpoint,
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("PUBLIC_ENDPOINT without endpoint: %v", err)
	}

	if err := m.UpdateCustomKeyStore(ctx, driver.UpdateCustomKeyStoreInput{
		CustomKeyStoreID: id, XksProxyConnectivity: driver.XksConnectivityPublicEndpoint, XksProxyURIEndpoint: "https://pub.example.com",
	}); err != nil {
		t.Fatalf("switch to PUBLIC_ENDPOINT: %v", err)
	}

	cfg = describeOne(t, m, id).XksProxyConfiguration
	if cfg.Connectivity != driver.XksConnectivityPublicEndpoint || cfg.URIEndpoint != "https://pub.example.com" ||
		cfg.VpcEndpointServiceName != "" || cfg.VpcEndpointServiceOwner != "" {
		t.Fatalf("after switch to PUBLIC = %+v", cfg)
	}
}

// TestDescribeCustomKeyStoresOrder: the list is ordered by creation date, then
// by ID for stores created at the same instant, so paging is stable. The
// newest store is created first, so map or insertion order can't produce the
// expected result by accident.
func TestDescribeCustomKeyStoresOrder(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Unix(0, 0).Add(time.Hour))
	m := kms.New(config.NewOptions(config.WithClock(clock), config.WithRegion("us-east-1"), config.WithAccountID("000000000000")))

	newest := newHSMStore(t, m, "newest", "cluster-cccccccccccc")

	clock.Set(time.Unix(0, 0))

	sameTime := []string{
		newHSMStore(t, m, "a", "cluster-aaaaaaaaaaaa"),
		newHSMStore(t, m, "b", "cluster-bbbbbbbbbbbb"),
	}

	xks, err := m.CreateCustomKeyStore(ctx, xksInput("c", "https://xks.example.com", "/kms/xks/v1"))
	if err != nil {
		t.Fatal(err)
	}

	sameTime = append(sameTime, xks)
	sort.Strings(sameTime)

	out, err := m.DescribeCustomKeyStores(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(out))
	for i := range out {
		got = append(got, out[i].CustomKeyStoreID)
	}

	want := append(sameTime, newest)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
