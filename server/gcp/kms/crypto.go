package kms

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
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

	algEd25519 = "EC_SIGN_ED25519"
	algP256    = "EC_SIGN_P256_SHA256"
	algP384    = "EC_SIGN_P384_SHA384"
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

// ensureMaterial generates the version's key on first use. Callers hold s.mu
// for writing.
func (v *versionModel) ensureMaterial() error {
	if v.secret != nil || v.priv != nil {
		return nil
	}

	alg := v.algorithm

	switch {
	case alg == algorithmSymmetric || strings.HasPrefix(alg, "AES_256"):
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

func (v *versionModel) randomSecret(n int) error {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return cerrors.Newf(cerrors.Internal, "generate key: %v", err)
	}

	v.secret = b

	return nil
}

// RSA modulus sizes Cloud KMS offers. Each is passed to rsa.GenerateKey as a
// constant, so no key is ever generated below 2048 bits.
const (
	rsaBits2048 = 2048
	rsaBits3072 = 3072
	rsaBits4096 = 4096
)

func (v *versionModel) generateRSA(alg string) error {
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

	v.priv = k

	return nil
}

func (v *versionModel) generateEC(alg string) error {
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

	v.priv = k

	return nil
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

func (v *versionModel) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(v.secret)
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
func (v *versionModel) seal(plaintext, aad []byte) ([]byte, error) {
	aead, err := v.gcm()
	if err != nil {
		return nil, err
	}

	id, err := strconv.ParseUint(v.id, 10, 32)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "version id %q: %v", v.id, err)
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

func (v *versionModel) open(ct, aad []byte) ([]byte, error) {
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
func (v *versionModel) sign(digest, data []byte) ([]byte, error) {
	switch k := v.priv.(type) {
	case ed25519.PrivateKey:
		return ed25519.Sign(k, data), nil
	case *rsa.PrivateKey:
		if strings.HasPrefix(v.algorithm, "RSA_SIGN_RAW_PKCS1") {
			return rsa.SignPKCS1v15(rand.Reader, k, 0, data)
		}

		if strings.HasPrefix(v.algorithm, "RSA_SIGN_PSS") {
			return rsa.SignPSS(rand.Reader, k, hashFor(v.algorithm), digest,
				&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}

		return rsa.SignPKCS1v15(rand.Reader, k, hashFor(v.algorithm), digest)
	case *ecdsa.PrivateKey:
		return ecdsa.SignASN1(rand.Reader, k, digest)
	default:
		return nil, errUnsupportedAlg(v.algorithm)
	}
}

// decryptOAEP is asymmetricDecrypt: RSA-OAEP with the algorithm's hash and an
// empty label, as Cloud KMS specifies.
func (v *versionModel) decryptOAEP(ct []byte) ([]byte, error) {
	k, ok := v.priv.(*rsa.PrivateKey)
	if !ok {
		return nil, errUnsupportedAlg(v.algorithm)
	}

	pt, err := rsa.DecryptOAEP(hashFor(v.algorithm).New(), nil, k, ct, nil)
	if err != nil {
		return nil, errInvalidCiphertext()
	}

	return pt, nil
}

// publicKeyPEM encodes the version's public key as a PKIX "PUBLIC KEY" PEM.
func (v *versionModel) publicKeyPEM() (string, error) {
	if v.priv == nil {
		return "", errUnsupportedAlg(v.algorithm)
	}

	der, err := x509.MarshalPKIXPublicKey(v.priv.Public())
	if err != nil {
		return "", cerrors.Newf(cerrors.Internal, "marshal public key: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}
