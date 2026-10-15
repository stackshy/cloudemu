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

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKDeriveSharedSecret: a real client creates a KEY_AGREEMENT key, derives
// a shared secret with a locally generated peer key, and checks it equals the
// secret the peer computes from the key's downloaded public key.
func TestSDKDeriveSharedSecret(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeKeyAgreement,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	md := key.KeyMetadata
	if md.KeyUsage != kmstypes.KeyUsageTypeKeyAgreement ||
		len(md.KeyAgreementAlgorithms) != 1 || md.KeyAgreementAlgorithms[0] != kmstypes.KeyAgreementAlgorithmSpecEcdh {
		t.Fatalf("metadata = usage %s, KeyAgreementAlgorithms %v", md.KeyUsage, md.KeyAgreementAlgorithms)
	}

	peer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	peerDER, err := x509.MarshalPKIXPublicKey(&peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	out, err := c.DeriveSharedSecret(ctx, &awskms.DeriveSharedSecretInput{
		KeyId: md.KeyId, KeyAgreementAlgorithm: kmstypes.KeyAgreementAlgorithmSpecEcdh, PublicKey: peerDER,
	})
	if err != nil {
		t.Fatalf("DeriveSharedSecret: %v", err)
	}

	if out.KeyAgreementAlgorithm != kmstypes.KeyAgreementAlgorithmSpecEcdh || out.KeyOrigin != kmstypes.OriginTypeAwsKms {
		t.Fatalf("response = %+v", out)
	}

	pub, err := c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: md.KeyId})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	if len(pub.KeyAgreementAlgorithms) != 1 {
		t.Fatalf("GetPublicKey KeyAgreementAlgorithms = %v", pub.KeyAgreementAlgorithms)
	}

	parsed, err := x509.ParsePKIXPublicKey(pub.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	kmsPub, err := parsed.(*ecdsa.PublicKey).ECDH()
	if err != nil {
		t.Fatal(err)
	}

	peerECDH, err := peer.ECDH()
	if err != nil {
		t.Fatal(err)
	}

	want, err := peerECDH.ECDH(kmsPub)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(out.SharedSecret, want) {
		t.Fatalf("shared secret mismatch:\n kms   %x\n local %x", out.SharedSecret, want)
	}

	// Wrong key usage → InvalidKeyUsageException.
	signKey, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.DeriveSharedSecret(ctx, &awskms.DeriveSharedSecretInput{
		KeyId: signKey.KeyMetadata.KeyId, KeyAgreementAlgorithm: kmstypes.KeyAgreementAlgorithmSpecEcdh, PublicKey: peerDER,
	})

	var usageErr *kmstypes.InvalidKeyUsageException
	if !errors.As(err, &usageErr) {
		t.Fatalf("sign key: err = %v, want InvalidKeyUsageException", err)
	}
}

// TestSDKReservedTagPrefix: tags in the aws: namespace fail with TagException
// on both CreateKey and TagResource.
func TestSDKReservedTagPrefix(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	_, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		Tags: []kmstypes.Tag{{TagKey: aws.String("aws:owner"), TagValue: aws.String("x")}},
	})

	var tagErr *kmstypes.TagException
	if !errors.As(err, &tagErr) {
		t.Fatalf("CreateKey: err = %v, want TagException", err)
	}

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.TagResource(ctx, &awskms.TagResourceInput{
		KeyId: key.KeyMetadata.KeyId,
		Tags:  []kmstypes.Tag{{TagKey: aws.String("aws:owner"), TagValue: aws.String("x")}},
	})
	if !errors.As(err, &tagErr) {
		t.Fatalf("TagResource: err = %v, want TagException", err)
	}
}

// TestSDKCreateGrantInvalidPrincipal: a non-ARN grantee fails with
// InvalidArnException, while a role ARN succeeds.
func TestSDKCreateGrantInvalidPrincipal(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.CreateGrant(ctx, &awskms.CreateGrantInput{
		KeyId: key.KeyMetadata.KeyId, GranteePrincipal: aws.String("not-an-arn"),
		Operations: []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt},
	})

	var arnErr *kmstypes.InvalidArnException
	if !errors.As(err, &arnErr) {
		t.Fatalf("err = %v, want InvalidArnException", err)
	}

	if _, err := c.CreateGrant(ctx, &awskms.CreateGrantInput{
		KeyId: key.KeyMetadata.KeyId, GranteePrincipal: aws.String("arn:aws:iam::111122223333:role/app"),
		Operations: []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt},
	}); err != nil {
		t.Fatalf("valid role ARN: %v", err)
	}
}

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
