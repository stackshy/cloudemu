package kms

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	// ciphertextMagic tags the emulator's symmetric ciphertext layout:
	// magic(1) | version id (uint32 big-endian) | GCM nonce | sealed bytes.
	// Carrying the version id lets decrypt pick the right key after rotation.
	ciphertextMagic  byte = 0x4b
	ciphertextHeader      = 1 + 4
	aes256KeyBytes        = 32
	aes128KeyBytes        = 16

	algEd25519   = "EC_SIGN_ED25519"
	algSymmetric = "GOOGLE_SYMMETRIC_ENCRYPTION"
	algP256      = "EC_SIGN_P256_SHA256"
	algP384      = "EC_SIGN_P384_SHA384"
)

// errUnsupportedAlg reports an algorithm the emulator cannot back with real
// Go stdlib crypto (for example secp256k1 or external keys).
func errUnsupportedAlg(alg string) error {
	return cerrors.Newf(cerrors.FailedPrecondition,
		"algorithm %s is not supported by the emulator's data plane", alg)
}

// errInvalidCiphertext is the error real Cloud KMS returns when a ciphertext
// fails to authenticate (tampered bytes, wrong key or AAD mismatch).
func errInvalidCiphertext() error {
	return cerrors.New(cerrors.InvalidArgument, "Decryption failed: the ciphertext is invalid.")
}

// ensureMaterial generates the version's key on first use. Callers hold m.mu
// and store v afterwards.
func (v *Version) ensureMaterial() error {
	if v.Secret != nil || v.PrivateKey != nil {
		return nil
	}

	alg := v.Algorithm

	switch {
	case alg == algSymmetric || strings.HasPrefix(alg, "AES_256"):
		return v.randomSecret(aes256KeyBytes)
	case strings.HasPrefix(alg, "AES_128"):
		return v.randomSecret(aes128KeyBytes)
	case strings.HasPrefix(alg, "HMAC_"):
		return v.randomSecret(hashFor(alg).Size())
	case strings.HasPrefix(alg, "RSA_"):
		return v.generateRSA(alg)
	default:
		return v.generateEC(alg)
	}
}

func (v *Version) randomSecret(n int) error {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return cerrors.Newf(cerrors.Internal, "generate key: %v", err)
	}

	v.Secret = b

	return nil
}

// RSA modulus sizes Cloud KMS offers. Each is passed to rsa.GenerateKey as a
// constant, so no key is ever generated below 2048 bits.
const (
	rsaBits2048 = 2048
	rsaBits3072 = 3072
	rsaBits4096 = 4096
)

func (v *Version) generateRSA(alg string) error {
	var (
		k   *rsa.PrivateKey
		err error
	)

	switch {
	case strings.Contains(alg, "_4096"):
		k, err = rsa.GenerateKey(rand.Reader, rsaBits4096)
	case strings.Contains(alg, "_3072"):
		k, err = rsa.GenerateKey(rand.Reader, rsaBits3072)
	case strings.Contains(alg, "_2048"):
		k, err = rsa.GenerateKey(rand.Reader, rsaBits2048)
	default:
		return errUnsupportedAlg(alg)
	}

	if err != nil {
		return cerrors.Newf(cerrors.Internal, "generate RSA key: %v", err)
	}

	return v.setPrivate(k)
}

func (v *Version) generateEC(alg string) error {
	var (
		k   crypto.Signer
		err error
	)

	switch alg {
	case algP256:
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case algP384:
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case algEd25519:
		_, k, err = ed25519.GenerateKey(rand.Reader)
	default:
		return errUnsupportedAlg(alg)
	}

	if err != nil {
		return cerrors.Newf(cerrors.Internal, "generate key: %v", err)
	}

	return v.setPrivate(k)
}

// hashFor returns the digest an algorithm name ends in (SHA256 by default).
func hashFor(alg string) crypto.Hash {
	switch {
	case strings.HasSuffix(alg, "SHA512"):
		return crypto.SHA512
	case strings.HasSuffix(alg, "SHA384"):
		return crypto.SHA384
	case strings.HasSuffix(alg, "SHA224"):
		return crypto.SHA224
	case strings.HasSuffix(alg, "SHA1"):
		return crypto.SHA1
	default:
		return crypto.SHA256
	}
}

func (v *Version) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(v.Secret)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "aes: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "gcm: %v", err)
	}

	return aead, nil
}

// seal encrypts plaintext with AES-GCM, binding aad, and prefixes the version
// id so decrypt can find this version later.
func (v *Version) seal(plaintext, aad []byte) ([]byte, error) {
	aead, err := v.gcm()
	if err != nil {
		return nil, err
	}

	id, err := strconv.ParseUint(v.ID, 10, 32)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "version id %q: %v", v.ID, err)
	}

	out := make([]byte, 0, ciphertextHeader+aead.NonceSize()+len(plaintext)+aead.Overhead())
	out = append(out, ciphertextMagic)
	out = binary.BigEndian.AppendUint32(out, uint32(id))

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "nonce: %v", err)
	}

	out = append(out, nonce...)

	return aead.Seal(out, nonce, plaintext, aad), nil
}

// ciphertextVersion reads the version id a ciphertext was sealed under.
func ciphertextVersion(ct []byte) (string, bool) {
	if len(ct) < ciphertextHeader || ct[0] != ciphertextMagic {
		return "", false
	}

	return strconv.FormatUint(uint64(binary.BigEndian.Uint32(ct[1:ciphertextHeader])), 10), true
}

func (v *Version) open(ct, aad []byte) ([]byte, error) {
	aead, err := v.gcm()
	if err != nil {
		return nil, err
	}

	body := ct[ciphertextHeader:]
	if len(body) < aead.NonceSize() {
		return nil, errInvalidCiphertext()
	}

	pt, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], aad)
	if err != nil {
		return nil, errInvalidCiphertext()
	}

	return pt, nil
}

// sign produces an asymmetricSign signature. digest is the pre-hashed input;
// data is the raw input, required for Ed25519 and RSA_SIGN_RAW_PKCS1_*.
func (v *Version) sign(digest, data []byte) ([]byte, error) {
	priv, err := v.signer()
	if err != nil {
		return nil, err
	}

	switch k := priv.(type) {
	case ed25519.PrivateKey:
		return ed25519.Sign(k, data), nil
	case *rsa.PrivateKey:
		if strings.HasPrefix(v.Algorithm, "RSA_SIGN_RAW_PKCS1") {
			return rsa.SignPKCS1v15(rand.Reader, k, 0, data)
		}

		if strings.HasPrefix(v.Algorithm, "RSA_SIGN_PSS") {
			return rsa.SignPSS(rand.Reader, k, hashFor(v.Algorithm), digest,
				&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}

		return rsa.SignPKCS1v15(rand.Reader, k, hashFor(v.Algorithm), digest)
	case *ecdsa.PrivateKey:
		return ecdsa.SignASN1(rand.Reader, k, digest)
	default:
		return nil, errUnsupportedAlg(v.Algorithm)
	}
}

// decryptOAEP is asymmetricDecrypt: RSA-OAEP with the algorithm's hash and an
// empty label, as Cloud KMS specifies.
func (v *Version) decryptOAEP(ct []byte) ([]byte, error) {
	priv, err := v.signer()
	if err != nil {
		return nil, err
	}

	k, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, errUnsupportedAlg(v.Algorithm)
	}

	pt, err := rsa.DecryptOAEP(hashFor(v.Algorithm).New(), nil, k, ct, nil)
	if err != nil {
		return nil, errInvalidCiphertext()
	}

	return pt, nil
}

// publicKeyPEM encodes the version's public key as a PKIX "PUBLIC KEY" PEM.
func (v *Version) publicKeyPEM() (string, error) {
	priv, err := v.signer()
	if err != nil {
		return "", err
	}

	der, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		return "", cerrors.Newf(cerrors.Internal, "marshal public key: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// mac is macSign: HMAC of data under the version's secret.
func (v *Version) mac(data []byte) []byte {
	mac := hmac.New(hashFor(v.Algorithm).New, v.Secret)
	mac.Write(data)

	return mac.Sum(nil)
}

// setPrivate stores k as PKCS#8 DER, the form snapshots persist.
func (v *Version) setPrivate(k crypto.Signer) error {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "marshal private key: %v", err)
	}

	v.PrivateKey = der

	return nil
}

// signer parses the version's PKCS#8 private key.
func (v *Version) signer() (crypto.Signer, error) {
	if v.PrivateKey == nil {
		return nil, errUnsupportedAlg(v.Algorithm)
	}

	k, err := x509.ParsePKCS8PrivateKey(v.PrivateKey)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "parse private key: %v", err)
	}

	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, errUnsupportedAlg(v.Algorithm)
	}

	return s, nil
}
