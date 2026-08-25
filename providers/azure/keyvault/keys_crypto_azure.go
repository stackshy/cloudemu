package keyvault

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // RSA-OAEP (SHA-1) is a Key Vault wire algorithm we must emulate
	"crypto/sha256"
	_ "crypto/sha512" // registers SHA-384/512 for RSA-PSS sign/verify
	"hash"
	"math/big"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	algRSAOAEP    = "RSA-OAEP"
	algRSAOAEP256 = "RSA-OAEP-256"
	algRSA15      = "RSA1_5"

	algRS256 = "RS256"
	algRS384 = "RS384"
	algRS512 = "RS512"
	algPS256 = "PS256"
	algPS384 = "PS384"
	algPS512 = "PS512"
	algES256 = "ES256"
	algES384 = "ES384"
	algES512 = "ES512"

	algA128KW = "A128KW"
	algA192KW = "A192KW"
	algA256KW = "A256KW"

	kindRSA = "rsa"
	kindPSS = "pss"
	kindEC  = "ec"
)

// opKey resolves a live, enabled key version and checks that op is permitted.
func (m *Mock) opKey(vault, name, version, op string) (*keyVersion, error) {
	kd := liveKey(m.vault(vault).keys, name)
	if kd == nil {
		return nil, errors.Newf(errors.NotFound, "key %q not found", name)
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	v := findKeyVersion(kd, version)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for key %q", version, name)
	}

	if !v.enabled {
		return nil, errors.Newf(errors.FailedPrecondition, "key %q is disabled", name)
	}

	if !opAllowed(v.keyOps, op) {
		return nil, errors.Newf(errors.PermissionDenied, "operation %q is not permitted on key %q", op, name)
	}

	return v, nil
}

func opAllowed(ops []string, op string) bool {
	for _, o := range ops {
		if o == op {
			return true
		}
	}

	return false
}

func rsaOAEPHash(alg string) (hash.Hash, bool) {
	switch alg {
	case algRSAOAEP:
		return sha1.New(), true //nolint:gosec // RSA-OAEP (SHA-1) is a Key Vault wire algorithm we must emulate
	case algRSAOAEP256:
		return sha256.New(), true
	default:
		return nil, false
	}
}

func result(v *keyVersion, out []byte) *driver.KVCryptoResult {
	return &driver.KVCryptoResult{Version: v.versionID, Value: out}
}

// EncryptKey encrypts a single block with the key's public part.
func (m *Mock) EncryptKey(_ context.Context, vault, name, version string, p driver.KVCryptoParams) (*driver.KVCryptoResult, error) {
	v, err := m.opKey(vault, name, version, "encrypt")
	if err != nil {
		return nil, err
	}

	if v.rsaKey == nil {
		return nil, errors.Newf(errors.InvalidArgument, "algorithm %q requires an RSA key", p.Algorithm)
	}

	out, err := rsaEncrypt(&v.rsaKey.PublicKey, p.Algorithm, p.Value)
	if err != nil {
		return nil, err
	}

	return result(v, out), nil
}

// DecryptKey decrypts a single block with the key's private part.
func (m *Mock) DecryptKey(_ context.Context, vault, name, version string, p driver.KVCryptoParams) (*driver.KVCryptoResult, error) {
	v, err := m.opKey(vault, name, version, "decrypt")
	if err != nil {
		return nil, err
	}

	if v.rsaKey == nil {
		return nil, errors.Newf(errors.InvalidArgument, "algorithm %q requires an RSA key", p.Algorithm)
	}

	out, err := rsaDecrypt(v.rsaKey, p.Algorithm, p.Value)
	if err != nil {
		return nil, err
	}

	return result(v, out), nil
}

// WrapKey wraps a symmetric key. RSA keys wrap with the RSA algorithms; oct
// keys wrap with AES key wrap (RFC 3394).
func (m *Mock) WrapKey(_ context.Context, vault, name, version string, p driver.KVCryptoParams) (*driver.KVCryptoResult, error) {
	v, err := m.opKey(vault, name, version, "wrapKey")
	if err != nil {
		return nil, err
	}

	out, err := wrap(v, p, true)
	if err != nil {
		return nil, err
	}

	return result(v, out), nil
}

// UnwrapKey reverses WrapKey.
func (m *Mock) UnwrapKey(_ context.Context, vault, name, version string, p driver.KVCryptoParams) (*driver.KVCryptoResult, error) {
	v, err := m.opKey(vault, name, version, "unwrapKey")
	if err != nil {
		return nil, err
	}

	out, err := wrap(v, p, false)
	if err != nil {
		return nil, err
	}

	return result(v, out), nil
}

func wrap(v *keyVersion, p driver.KVCryptoParams, forward bool) ([]byte, error) {
	switch {
	case v.rsaKey != nil && forward:
		return rsaEncrypt(&v.rsaKey.PublicKey, p.Algorithm, p.Value)
	case v.rsaKey != nil:
		return rsaDecrypt(v.rsaKey, p.Algorithm, p.Value)
	case v.octKey != nil && forward:
		return aesKeyWrap(v.octKey, p.Algorithm, p.Value)
	case v.octKey != nil:
		return aesKeyUnwrap(v.octKey, p.Algorithm, p.Value)
	default:
		return nil, errors.Newf(errors.InvalidArgument, "algorithm %q is not supported for this key type", p.Algorithm)
	}
}

func rsaEncrypt(pub *rsa.PublicKey, alg string, msg []byte) ([]byte, error) {
	if alg == algRSA15 {
		return rsa.EncryptPKCS1v15(rand.Reader, pub, msg)
	}

	h, ok := rsaOAEPHash(alg)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "unsupported encryption algorithm %q", alg)
	}

	return rsa.EncryptOAEP(h, rand.Reader, pub, msg, nil)
}

func rsaDecrypt(key *rsa.PrivateKey, alg string, ct []byte) ([]byte, error) {
	if alg == algRSA15 {
		return rsa.DecryptPKCS1v15(rand.Reader, key, ct)
	}

	h, ok := rsaOAEPHash(alg)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "unsupported encryption algorithm %q", alg)
	}

	return rsa.DecryptOAEP(h, rand.Reader, key, ct, nil)
}

// SignKey signs a pre-computed digest and returns the signature.
func (m *Mock) SignKey(_ context.Context, vault, name, version string, p driver.KVCryptoParams) (*driver.KVCryptoResult, error) {
	v, err := m.opKey(vault, name, version, "sign")
	if err != nil {
		return nil, err
	}

	out, err := sign(v, p.Algorithm, p.Value)
	if err != nil {
		return nil, err
	}

	return result(v, out), nil
}

// VerifyKey checks a signature over a digest.
func (m *Mock) VerifyKey(_ context.Context, vault, name, version string, p driver.KVCryptoParams) (bool, error) {
	v, err := m.opKey(vault, name, version, "verify")
	if err != nil {
		return false, err
	}

	return verify(v, p.Algorithm, p.Value, p.Signature)
}

// sigHash maps a signature algorithm to its digest hash and whether it is RSA,
// RSA-PSS or ECDSA.
func sigHash(alg string) (crypto.Hash, string, bool) {
	switch alg {
	case algRS256:
		return crypto.SHA256, kindRSA, true
	case algPS256:
		return crypto.SHA256, kindPSS, true
	case algES256:
		return crypto.SHA256, kindEC, true
	case algRS384:
		return crypto.SHA384, kindRSA, true
	case algPS384:
		return crypto.SHA384, kindPSS, true
	case algES384:
		return crypto.SHA384, kindEC, true
	case algRS512:
		return crypto.SHA512, kindRSA, true
	case algPS512:
		return crypto.SHA512, kindPSS, true
	case algES512:
		return crypto.SHA512, kindEC, true
	default:
		return 0, "", false
	}
}

func sign(v *keyVersion, alg string, digest []byte) ([]byte, error) {
	h, kind, ok := sigHash(alg)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "unsupported signature algorithm %q", alg)
	}

	switch kind {
	case kindRSA:
		if v.rsaKey == nil {
			return nil, errors.Newf(errors.InvalidArgument, "algorithm %q requires an RSA key", alg)
		}

		return rsa.SignPKCS1v15(rand.Reader, v.rsaKey, h, digest)
	case kindPSS:
		if v.rsaKey == nil {
			return nil, errors.Newf(errors.InvalidArgument, "algorithm %q requires an RSA key", alg)
		}

		return rsa.SignPSS(rand.Reader, v.rsaKey, h, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	default:
		return ecSign(v, digest)
	}
}

func ecSign(v *keyVersion, digest []byte) ([]byte, error) {
	if v.ecKey == nil {
		return nil, errors.New(errors.InvalidArgument, "ECDSA algorithm requires an EC key")
	}

	r, s, err := ecdsa.Sign(rand.Reader, v.ecKey, digest)
	if err != nil {
		return nil, err
	}

	// Key Vault returns EC signatures as the raw r||s concatenation, each
	// padded to the curve's coordinate length (IEEE P1363), not ASN.1 DER.
	size := (v.ecKey.Curve.Params().BitSize + 7) / 8

	return append(padLeft(r.Bytes(), size), padLeft(s.Bytes(), size)...), nil
}

func verify(v *keyVersion, alg string, digest, sig []byte) (bool, error) {
	h, kind, ok := sigHash(alg)
	if !ok {
		return false, errors.Newf(errors.InvalidArgument, "unsupported signature algorithm %q", alg)
	}

	switch kind {
	case kindRSA:
		if v.rsaKey == nil {
			return false, errors.Newf(errors.InvalidArgument, "algorithm %q requires an RSA key", alg)
		}

		return rsa.VerifyPKCS1v15(&v.rsaKey.PublicKey, h, digest, sig) == nil, nil
	case kindPSS:
		if v.rsaKey == nil {
			return false, errors.Newf(errors.InvalidArgument, "algorithm %q requires an RSA key", alg)
		}

		return rsa.VerifyPSS(&v.rsaKey.PublicKey, h, digest, sig, nil) == nil, nil
	default:
		return ecVerify(v, digest, sig)
	}
}

func ecVerify(v *keyVersion, digest, sig []byte) (bool, error) {
	if v.ecKey == nil {
		return false, errors.New(errors.InvalidArgument, "ECDSA algorithm requires an EC key")
	}

	size := (v.ecKey.Curve.Params().BitSize + 7) / 8
	if len(sig) != 2*size {
		return false, nil
	}

	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])

	return ecdsa.Verify(&v.ecKey.PublicKey, digest, r, s), nil
}
