package kms

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"hash"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func pairSpecToKMSSpec(spec string) (string, error) {
	switch spec {
	case driver.DataKeyPairRSA2048, driver.DataKeyPairRSA3072, driver.DataKeyPairRSA4096,
		driver.DataKeyPairECCNISTP256, driver.DataKeyPairECCNISTP384, driver.DataKeyPairECCNISTP521:
		return spec, nil
	default:
		return "", errors.Newf(errors.InvalidArgument, "unsupported KeyPairSpec %q", spec)
	}
}

// GenerateDataKeyPair returns a fresh asymmetric data key pair: DER public key,
// PKCS#8 DER private key (plaintext), and the private key encrypted under the
// KMS key.
func (m *Mock) GenerateDataKeyPair(
	ctx context.Context, in driver.GenerateDataKeyPairInput,
) (*driver.GenerateDataKeyPairOutput, error) {
	spec, err := pairSpecToKMSSpec(in.KeyPairSpec)
	if err != nil {
		return nil, err
	}

	priv, err := generateAsymmetric(spec)
	if err != nil {
		return nil, err
	}

	pubDER, err := x509.MarshalPKIXPublicKey(publicKeyOf(priv))
	if err != nil {
		return nil, errors.Newf(errors.Internal, "marshal public key: %v", err)
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "marshal private key: %v", err)
	}

	enc, err := m.Encrypt(ctx, driver.EncryptInput{
		KeyID: in.KeyID, Plaintext: privDER, EncryptionContext: in.EncryptionContext,
	})
	if err != nil {
		return nil, err
	}

	return &driver.GenerateDataKeyPairOutput{
		KeyID:                    enc.KeyID,
		KeyPairSpec:              in.KeyPairSpec,
		PublicKey:                pubDER,
		PrivateKeyPlaintext:      privDER,
		PrivateKeyCiphertextBlob: enc.CiphertextBlob,
	}, nil
}

// GenerateDataKeyPairWithoutPlaintext is GenerateDataKeyPair without the
// plaintext private key.
func (m *Mock) GenerateDataKeyPairWithoutPlaintext(
	ctx context.Context, in driver.GenerateDataKeyPairInput,
) (*driver.GenerateDataKeyPairOutput, error) {
	out, err := m.GenerateDataKeyPair(ctx, in)
	if err != nil {
		return nil, err
	}

	out.PrivateKeyPlaintext = nil

	return out, nil
}

// GetPublicKey returns the DER-encoded (SPKI, RFC 5280) public key of an
// asymmetric key, along with its spec/usage and the algorithms it supports so a
// caller can encrypt or verify offline. Symmetric (and other keys with no
// public half) are rejected with UnsupportedOperationException, matching real
// KMS.
func (m *Mock) GetPublicKey(_ context.Context, in driver.GetPublicKeyInput) (*driver.GetPublicKeyOutput, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := m.requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	pub := publicKeyOf(kd.privKey)
	if pub == nil {
		return nil, driver.ErrUnsupportedOperation
	}

	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "marshal public key: %v", err)
	}

	return &driver.GetPublicKeyOutput{
		KeyID:                kd.meta.KeyID,
		PublicKey:            pubDER,
		KeySpec:              kd.meta.KeySpec,
		KeyUsage:             kd.meta.KeyUsage,
		EncryptionAlgorithms: driver.EncryptionAlgorithmsFor(kd.meta.KeySpec, kd.meta.KeyUsage),
		SigningAlgorithms:    driver.SigningAlgorithmsFor(kd.meta.KeySpec, kd.meta.KeyUsage),

		KeyAgreementAlgorithms: driver.KeyAgreementAlgorithmsFor(kd.meta.KeySpec, kd.meta.KeyUsage),
	}, nil
}

// DeriveSharedSecret performs ECDH between the private half of a KEY_AGREEMENT
// ECC key and the peer's public key (DER SubjectPublicKeyInfo on the same
// curve), returning the raw shared secret: the x-coordinate of the shared
// point, exactly what the peer computes locally with its own private key and
// this key's public key (GetPublicKey).
func (m *Mock) DeriveSharedSecret(
	_ context.Context, in driver.DeriveSharedSecretInput,
) (*driver.DeriveSharedSecretOutput, error) {
	if in.KeyAgreementAlgorithm != driver.KeyAgreementECDH {
		return nil, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'keyAgreementAlgorithm' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [ECDH]", in.KeyAgreementAlgorithm)
	}

	if len(in.PublicKey) == 0 {
		return nil, errors.New(errors.InvalidArgument, "PublicKey is required")
	}

	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := m.requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	if kd.meta.KeyUsage != driver.UsageKeyAgreement {
		return nil, driver.ErrInvalidKeyUsage
	}

	priv, ok := kd.privKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, driver.ErrInvalidKeyUsage
	}

	secret, err := ecdhSharedSecret(priv, in.PublicKey)
	if err != nil {
		return nil, err
	}

	return &driver.DeriveSharedSecretOutput{
		KeyID:                 kd.meta.KeyID,
		SharedSecret:          secret,
		KeyAgreementAlgorithm: driver.KeyAgreementECDH,
		KeyOrigin:             kd.meta.Origin,
	}, nil
}

// ecdhSharedSecret computes the ECDH shared secret of priv and a peer public
// key given as DER SubjectPublicKeyInfo. The peer key must be an EC key on the
// same curve as priv.
func ecdhSharedSecret(priv *ecdsa.PrivateKey, peerDER []byte) ([]byte, error) {
	parsed, err := x509.ParsePKIXPublicKey(peerDER)
	if err != nil {
		return nil, errors.New(errors.InvalidArgument,
			"PublicKey must be a DER-encoded X.509 public key (SubjectPublicKeyInfo, RFC 5280)")
	}

	peer, ok := parsed.(*ecdsa.PublicKey)
	if !ok || peer.Curve != priv.Curve {
		return nil, errors.New(errors.InvalidArgument,
			"PublicKey must be an elliptic curve public key on the same curve as the KMS key")
	}

	ecdhPriv, err := priv.ECDH()
	if err != nil {
		return nil, errors.Newf(errors.Internal, "convert private key: %v", err)
	}

	ecdhPeer, err := peer.ECDH()
	if err != nil {
		return nil, errors.New(errors.InvalidArgument, "PublicKey is not a valid point on the key's curve")
	}

	secret, err := ecdhPriv.ECDH(ecdhPeer)
	if err != nil {
		return nil, errors.Newf(errors.InvalidArgument, "derive shared secret: %v", err)
	}

	return secret, nil
}

func publicKeyOf(priv crypto.PrivateKey) crypto.PublicKey {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}

// hashForAlg maps a signing/MAC algorithm suffix to its hash.
func hashForAlg(alg string) (crypto.Hash, func() hash.Hash, error) {
	switch {
	case strings.HasSuffix(alg, "_224"):
		return crypto.SHA224, sha256.New224, nil
	case strings.HasSuffix(alg, "_256"):
		return crypto.SHA256, sha256.New, nil
	case strings.HasSuffix(alg, "_384"):
		return crypto.SHA384, sha512.New384, nil
	case strings.HasSuffix(alg, "_512"):
		return crypto.SHA512, sha512.New, nil
	default:
		return 0, nil, errors.Newf(errors.InvalidArgument, "unsupported algorithm %q", alg)
	}
}

// digestFor returns the digest to sign: the message itself when it is already a
// DIGEST, otherwise the hash of the raw message.
func digestFor(messageType string, message []byte, h func() hash.Hash) []byte {
	if messageType == driver.MessageTypeDigest {
		return message
	}

	hh := h()
	hh.Write(message)

	return hh.Sum(nil)
}

// signingAlgForSpec reports whether a signing algorithm is valid for a key
// spec: RSA keys sign with RSASSA_* algorithms, ECC keys with ECDSA_*. This
// rejects e.g. signing an ECC key with RSASSA_PSS (which would otherwise return
// an ECDSA signature mislabeled as RSA).
func signingAlgForSpec(spec, alg string) bool {
	switch {
	case strings.HasPrefix(spec, "RSA_"):
		return strings.HasPrefix(alg, "RSASSA_")
	case strings.HasPrefix(spec, "ECC_"):
		return strings.HasPrefix(alg, "ECDSA_")
	default:
		return false
	}
}

// Sign signs a message (or pre-computed digest) with an asymmetric key.
func (m *Mock) Sign(_ context.Context, in driver.SignInput) (*driver.SignOutput, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := m.requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	if kd.meta.KeyUsage != driver.UsageSignVerify {
		return nil, driver.ErrInvalidKeyUsage
	}

	if !signingAlgForSpec(kd.meta.KeySpec, in.SigningAlgorithm) {
		return nil, driver.ErrInvalidKeyUsage
	}

	hashAlg, hCtor, err := hashForAlg(in.SigningAlgorithm)
	if err != nil {
		return nil, err
	}

	digest := digestFor(in.MessageType, in.Message, hCtor)

	sig, err := signDigest(kd.privKey, in.SigningAlgorithm, hashAlg, digest)
	if err != nil {
		return nil, err
	}

	return &driver.SignOutput{KeyID: kd.meta.KeyID, Signature: sig, SigningAlgorithm: in.SigningAlgorithm}, nil
}

func signDigest(priv crypto.PrivateKey, alg string, hashAlg crypto.Hash, digest []byte) ([]byte, error) {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		if strings.HasPrefix(alg, "RSASSA_PSS") {
			return rsa.SignPSS(rand.Reader, k, hashAlg, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}

		return rsa.SignPKCS1v15(rand.Reader, k, hashAlg, digest)
	case *ecdsa.PrivateKey:
		return ecdsa.SignASN1(rand.Reader, k, digest)
	default:
		return nil, errors.New(errors.InvalidArgument, "key is not an asymmetric signing key")
	}
}

// Verify checks a signature against a message (or digest).
//
//nolint:gocritic // in is the public Verify input, taken by value to match the driver API
func (m *Mock) Verify(_ context.Context, in driver.VerifyInput) (*driver.VerifyOutput, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := m.requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	if kd.meta.KeyUsage != driver.UsageSignVerify {
		return nil, driver.ErrInvalidKeyUsage
	}

	if !signingAlgForSpec(kd.meta.KeySpec, in.SigningAlgorithm) {
		return nil, driver.ErrInvalidKeyUsage
	}

	hashAlg, hCtor, err := hashForAlg(in.SigningAlgorithm)
	if err != nil {
		return nil, err
	}

	digest := digestFor(in.MessageType, in.Message, hCtor)
	valid := verifyDigest(kd.privKey, in.SigningAlgorithm, hashAlg, digest, in.Signature)

	if !valid {
		return nil, driver.ErrSignatureInvalid
	}

	return &driver.VerifyOutput{KeyID: kd.meta.KeyID, SignatureValid: true, SigningAlgorithm: in.SigningAlgorithm}, nil
}

func verifyDigest(priv crypto.PrivateKey, alg string, hashAlg crypto.Hash, digest, sig []byte) bool {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		if strings.HasPrefix(alg, "RSASSA_PSS") {
			return rsa.VerifyPSS(&k.PublicKey, hashAlg, digest, sig,
				&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		}

		return rsa.VerifyPKCS1v15(&k.PublicKey, hashAlg, digest, sig) == nil
	case *ecdsa.PrivateKey:
		return ecdsa.VerifyASN1(&k.PublicKey, digest, sig)
	default:
		return false
	}
}

// GenerateMac computes an HMAC of the message with an HMAC key.
func (m *Mock) GenerateMac(_ context.Context, in driver.GenerateMacInput) (*driver.GenerateMacOutput, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := m.requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	if kd.meta.KeyUsage != driver.UsageGenerateVerifyMac {
		return nil, driver.ErrInvalidKeyUsage
	}

	mac, err := computeMac(kd.currentMaterial(), in.MacAlgorithm, in.Message)
	if err != nil {
		return nil, err
	}

	return &driver.GenerateMacOutput{KeyID: kd.meta.KeyID, Mac: mac, MacAlgorithm: in.MacAlgorithm}, nil
}

// VerifyMac recomputes and constant-time compares a MAC.
//
//nolint:gocritic // in is the public VerifyMac input, taken by value to match the driver API
func (m *Mock) VerifyMac(_ context.Context, in driver.VerifyMacInput) (*driver.VerifyMacOutput, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := m.requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	if kd.meta.KeyUsage != driver.UsageGenerateVerifyMac {
		return nil, driver.ErrInvalidKeyUsage
	}

	want, err := computeMac(kd.currentMaterial(), in.MacAlgorithm, in.Message)
	if err != nil {
		return nil, err
	}

	if !hmac.Equal(want, in.Mac) {
		return nil, driver.ErrMacInvalid
	}

	return &driver.VerifyMacOutput{KeyID: kd.meta.KeyID, MacValid: true, MacAlgorithm: in.MacAlgorithm}, nil
}

func computeMac(material []byte, alg string, message []byte) ([]byte, error) {
	_, hCtor, err := hashForAlg(alg)
	if err != nil {
		return nil, err
	}

	mac := hmac.New(hCtor, material)
	mac.Write(message)

	return mac.Sum(nil), nil
}
