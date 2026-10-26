package kms_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKCustomKeyStoreLifecycle drives the six custom key store operations
// (plus CreateKey in a store) through the real client, including the typed
// state errors.
func TestSDKCustomKeyStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	create := func(name, cluster string) (*awskms.CreateCustomKeyStoreOutput, error) {
		return c.CreateCustomKeyStore(ctx, &awskms.CreateCustomKeyStoreInput{
			CustomKeyStoreName:     aws.String(name),
			CloudHsmClusterId:      aws.String(cluster),
			TrustAnchorCertificate: aws.String("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"),
			KeyStorePassword:       aws.String("kmsPswd1"),
		})
	}

	created, err := create("hsm", "cluster-abcdefghijk")
	if err != nil {
		t.Fatalf("CreateCustomKeyStore: %v", err)
	}

	id := created.CustomKeyStoreId

	var nameErr *kmstypes.CustomKeyStoreNameInUseException
	if _, err := create("hsm", "cluster-zzzzzzzzzzz"); !errors.As(err, &nameErr) {
		t.Fatalf("dup name: err = %v", err)
	}

	var clusterErr *kmstypes.CloudHsmClusterInUseException
	if _, err := create("hsm2", "cluster-abcdefghijk"); !errors.As(err, &clusterErr) {
		t.Fatalf("dup cluster: err = %v", err)
	}

	desc, err := c.DescribeCustomKeyStores(ctx, &awskms.DescribeCustomKeyStoresInput{CustomKeyStoreId: id})
	if err != nil || len(desc.CustomKeyStores) != 1 {
		t.Fatalf("Describe: %v, %v", desc, err)
	}

	s := desc.CustomKeyStores[0]
	if s.ConnectionState != kmstypes.ConnectionStateTypeDisconnected ||
		s.CustomKeyStoreType != kmstypes.CustomKeyStoreTypeAwsCloudhsm || aws.ToString(s.CloudHsmClusterId) != "cluster-abcdefghijk" {
		t.Fatalf("store = %+v", s)
	}

	var stateErr *kmstypes.CustomKeyStoreInvalidStateException

	_, err = c.CreateKey(ctx, &awskms.CreateKeyInput{Origin: kmstypes.OriginTypeAwsCloudhsm, CustomKeyStoreId: id})
	if !errors.As(err, &stateErr) {
		t.Fatalf("CreateKey in disconnected store: %v", err)
	}

	if _, err := c.DisconnectCustomKeyStore(ctx, &awskms.DisconnectCustomKeyStoreInput{CustomKeyStoreId: id}); !errors.As(err, &stateErr) {
		t.Fatalf("disconnect disconnected: %v", err)
	}

	if _, err := c.ConnectCustomKeyStore(ctx, &awskms.ConnectCustomKeyStoreInput{CustomKeyStoreId: id}); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Origin: kmstypes.OriginTypeAwsCloudhsm, CustomKeyStoreId: id})
	if err != nil {
		t.Fatalf("CreateKey in store: %v", err)
	}

	if aws.ToString(key.KeyMetadata.CustomKeyStoreId) != aws.ToString(id) ||
		aws.ToString(key.KeyMetadata.CloudHsmClusterId) != "cluster-abcdefghijk" {
		t.Fatalf("key metadata = %+v", key.KeyMetadata)
	}

	if _, err := c.UpdateCustomKeyStore(ctx, &awskms.UpdateCustomKeyStoreInput{
		CustomKeyStoreId: id, NewCustomKeyStoreName: aws.String("renamed"),
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if _, err := c.DeleteCustomKeyStore(ctx, &awskms.DeleteCustomKeyStoreInput{CustomKeyStoreId: id}); !errors.As(err, &stateErr) {
		t.Fatalf("delete connected: %v", err)
	}

	if _, err := c.DisconnectCustomKeyStore(ctx, &awskms.DisconnectCustomKeyStoreInput{CustomKeyStoreId: id}); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	var hasKeys *kmstypes.CustomKeyStoreHasCMKsException
	if _, err := c.DeleteCustomKeyStore(ctx, &awskms.DeleteCustomKeyStoreInput{CustomKeyStoreId: id}); !errors.As(err, &hasKeys) {
		t.Fatalf("delete with keys: %v", err)
	}

	var notFound *kmstypes.CustomKeyStoreNotFoundException
	if _, err := c.DescribeCustomKeyStores(ctx, &awskms.DescribeCustomKeyStoresInput{
		CustomKeyStoreId: aws.String("cks-00000000000000000"),
	}); !errors.As(err, &notFound) {
		t.Fatalf("describe missing: %v", err)
	}

	byName, err := c.DescribeCustomKeyStores(ctx, &awskms.DescribeCustomKeyStoresInput{CustomKeyStoreName: aws.String("renamed")})
	if err != nil || len(byName.CustomKeyStores) != 1 {
		t.Fatalf("describe by new name: %v, %v", byName, err)
	}
}

// TestSDKExternalKeyStore checks the XKS proxy configuration round-trips (without
// the secret) and the proxy URI uniqueness error.
func TestSDKExternalKeyStore(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	in := func(name string) *awskms.CreateCustomKeyStoreInput {
		return &awskms.CreateCustomKeyStoreInput{
			CustomKeyStoreName:   aws.String(name),
			CustomKeyStoreType:   kmstypes.CustomKeyStoreTypeExternalKeyStore,
			XksProxyUriEndpoint:  aws.String("https://xks.example.com"),
			XksProxyUriPath:      aws.String("/kms/xks/v1"),
			XksProxyConnectivity: kmstypes.XksProxyConnectivityTypePublicEndpoint,
			XksProxyAuthenticationCredential: &kmstypes.XksProxyAuthenticationCredentialType{
				AccessKeyId:        aws.String("ABCDEFGHIJKLMNOPQRST"),
				RawSecretAccessKey: aws.String("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ="),
			},
		}
	}

	created, err := c.CreateCustomKeyStore(ctx, in("xks"))
	if err != nil {
		t.Fatalf("CreateCustomKeyStore: %v", err)
	}

	var uriErr *kmstypes.XksProxyUriInUseException
	if _, err := c.CreateCustomKeyStore(ctx, in("xks2")); !errors.As(err, &uriErr) {
		t.Fatalf("dup uri: %v", err)
	}

	desc, err := c.DescribeCustomKeyStores(ctx, &awskms.DescribeCustomKeyStoresInput{CustomKeyStoreId: created.CustomKeyStoreId})
	if err != nil {
		t.Fatal(err)
	}

	cfg := desc.CustomKeyStores[0].XksProxyConfiguration
	if cfg == nil || aws.ToString(cfg.UriEndpoint) != "https://xks.example.com" ||
		aws.ToString(cfg.AccessKeyId) != "ABCDEFGHIJKLMNOPQRST" || cfg.Connectivity != kmstypes.XksProxyConnectivityTypePublicEndpoint {
		t.Fatalf("XksProxyConfiguration = %+v", cfg)
	}

	if _, err := c.ConnectCustomKeyStore(ctx, &awskms.ConnectCustomKeyStoreInput{CustomKeyStoreId: created.CustomKeyStoreId}); err != nil {
		t.Fatal(err)
	}

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		Origin: kmstypes.OriginTypeExternalKeyStore, CustomKeyStoreId: created.CustomKeyStoreId, XksKeyId: aws.String("ext-1"),
	})
	if err != nil {
		t.Fatalf("CreateKey XKS: %v", err)
	}

	if key.KeyMetadata.XksKeyConfiguration == nil || aws.ToString(key.KeyMetadata.XksKeyConfiguration.Id) != "ext-1" {
		t.Fatalf("XksKeyConfiguration = %+v", key.KeyMetadata.XksKeyConfiguration)
	}

	var inUse *kmstypes.XksKeyAlreadyInUseException
	if _, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		Origin: kmstypes.OriginTypeExternalKeyStore, CustomKeyStoreId: created.CustomKeyStoreId, XksKeyId: aws.String("ext-1"),
	}); !errors.As(err, &inUse) {
		t.Fatalf("dup xks key: %v", err)
	}
}

// TestSDKCustomKeyStoreKeyUnavailable: after DisconnectCustomKeyStore a real
// client sees the store's key as Unavailable, crypto fails with
// KMSInvalidStateException (also for a Disabled key), GenerateRandom with the
// store's ID fails with CustomKeyStoreInvalidStateException, and a
// multi-Region key in a store fails with UnsupportedOperationException.
func TestSDKCustomKeyStoreKeyUnavailable(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	created, err := c.CreateCustomKeyStore(ctx, &awskms.CreateCustomKeyStoreInput{
		CustomKeyStoreName:     aws.String("hsm"),
		CloudHsmClusterId:      aws.String("cluster-abcdefghijk"),
		TrustAnchorCertificate: aws.String("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"),
		KeyStorePassword:       aws.String("kmsPswd1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	id := created.CustomKeyStoreId

	var stateErr *kmstypes.CustomKeyStoreInvalidStateException
	if _, err := c.GenerateRandom(ctx, &awskms.GenerateRandomInput{
		NumberOfBytes: aws.Int32(8), CustomKeyStoreId: id,
	}); !errors.As(err, &stateErr) {
		t.Fatalf("GenerateRandom on disconnected store: %v", err)
	}

	if _, err := c.ConnectCustomKeyStore(ctx, &awskms.ConnectCustomKeyStoreInput{CustomKeyStoreId: id}); err != nil {
		t.Fatal(err)
	}

	rnd, err := c.GenerateRandom(ctx, &awskms.GenerateRandomInput{NumberOfBytes: aws.Int32(8), CustomKeyStoreId: id})
	if err != nil || len(rnd.Plaintext) != 8 {
		t.Fatalf("GenerateRandom on connected store = %v, %v", rnd, err)
	}

	var unsupported *kmstypes.UnsupportedOperationException
	if _, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		Origin: kmstypes.OriginTypeAwsCloudhsm, CustomKeyStoreId: id, MultiRegion: aws.Bool(true),
	}); !errors.As(err, &unsupported) || !strings.Contains(unsupported.ErrorMessage(), "single-Region symmetric") {
		t.Fatalf("multi-Region key in store: %v", err)
	}

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Origin: kmstypes.OriginTypeAwsCloudhsm, CustomKeyStoreId: id})
	if err != nil {
		t.Fatal(err)
	}

	keyID := key.KeyMetadata.KeyId

	if _, err := c.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: keyID}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.DisconnectCustomKeyStore(ctx, &awskms.DisconnectCustomKeyStoreInput{CustomKeyStoreId: id}); err != nil {
		t.Fatal(err)
	}

	desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: keyID})
	if err != nil {
		t.Fatal(err)
	}

	if desc.KeyMetadata.KeyState != kmstypes.KeyStateUnavailable || desc.KeyMetadata.Enabled {
		t.Fatalf("DescribeKey state = %s enabled=%v, want Unavailable", desc.KeyMetadata.KeyState, desc.KeyMetadata.Enabled)
	}

	var invalidState *kmstypes.KMSInvalidStateException

	_, err = c.Encrypt(ctx, &awskms.EncryptInput{KeyId: keyID, Plaintext: []byte("hi")})
	if !errors.As(err, &invalidState) || !strings.HasSuffix(invalidState.ErrorMessage(), "is unavailable") {
		t.Fatalf("Encrypt on unavailable key: %v", err)
	}
}
