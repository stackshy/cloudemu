package kms

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SHA-1 is required by the RSAES_OAEP_SHA_1 algorithm KMS defines
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// Ciphertext blob format (self-describing so Decrypt needs no key id for
// symmetric keys, matching real KMS):
//
//	magic(1) | keyIDLen(uint16 BE) | keyID | body
//
// magic 0x01 = AES-256-GCM (body = keyVersion(uint32 BE) | nonce(12) |
// ciphertext); the key version selects the exact backing-key bytes so rotation
// doesn't break old ciphertext, and the encryption context is bound as AES-GCM
// additional data. magic 0x02 = RSA-OAEP-SHA-256 (body = RSA ciphertext).
const (
	blobMagicGCM     = 0x01
	blobMagicRSAOAEP = 0x02
	gcmNonceSize     = 12
	keyVersionSize   = 4
	rsa2048Bits      = 2048
	rsa3072Bits      = 3072
	rsa4096Bits      = 4096
	aes256Bytes      = 32
	aes128Bytes      = 16
	maxRandomBytes   = 1024
	// maxPlaintextBytes is the KMS Encrypt Plaintext length constraint: real
	// KMS caps a single Encrypt at 4096 bytes (the SYMMETRIC_DEFAULT limit) and
	// rejects anything larger with ValidationException. Larger payloads must use
	// envelope encryption via GenerateDataKey.
	maxPlaintextBytes = 4096
)

func isAsymmetricSpec(spec string) bool {
	return strings.HasPrefix(spec, "RSA_") || strings.HasPrefix(spec, "ECC_")
}

func generateAsymmetric(spec string) (crypto.PrivateKey, error) {
	switch spec {
	case driver.SpecRSA2048:
		return rsa.GenerateKey(rand.Reader, rsa2048Bits)
	case driver.SpecRSA3072:
		return rsa.GenerateKey(rand.Reader, rsa3072Bits)
	case driver.SpecRSA4096:
		return rsa.GenerateKey(rand.Reader, rsa4096Bits)
	case driver.SpecECCNISTP256:
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case driver.SpecECCNISTP384:
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case driver.SpecECCNISTP521:
		return ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	default:
		return nil, errors.Newf(errors.InvalidArgument, "unsupported asymmetric spec %q", spec)
	}
}

// canonicalContext renders the encryption context as deterministic AEAD
// additional data. Each key and value is length-prefixed (not delimited by a
// separator) so distinct contexts can never collide into the same AAD — e.g.
// {"a=b":"c"} and {"a":"b=c"} produce different bytes, unlike a naive "k=v;"
// join.
func canonicalContext(ctx map[string]string) []byte {
	if len(ctx) == 0 {
		return nil
	}

	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b []byte

	for _, k := range keys {
		//nolint:gosec // encryption-context keys/values are small; length fits uint32
		b = binary.BigEndian.AppendUint32(b, uint32(len(k)))
		b = append(b, k...)
		//nolint:gosec // encryption-context keys/values are small; length fits uint32
		b = binary.BigEndian.AppendUint32(b, uint32(len(ctx[k])))
		b = append(b, ctx[k]...)
	}

	return b
}

func encodeBlob(magic byte, keyID string, body []byte) []byte {
	out := make([]byte, 0, 1+2+len(keyID)+len(body))
	out = append(out, magic)
	//nolint:gosec // key ID is a UUID (~36 bytes), always well under uint16 max
	out = binary.BigEndian.AppendUint16(out, uint16(len(keyID)))
	out = append(out, keyID...)
	out = append(out, body...)

	return out
}

func decodeBlob(blob []byte) (magic byte, keyID string, body []byte, err error) {
	const header = 3
	if len(blob) < header {
		return 0, "", nil, driver.ErrInvalidCiphertext
	}

	magic = blob[0]
	idLen := int(binary.BigEndian.Uint16(blob[1:header]))

	if len(blob) < header+idLen {
		return 0, "", nil, driver.ErrInvalidCiphertext
	}

	keyID = string(blob[header : header+idLen])
	body = blob[header+idLen:]

	return magic, keyID, body, nil
}

// symmetricGCM builds an AES-GCM AEAD from raw key material.
func symmetricGCM(material []byte) (cipher.AEAD, error) {
	if len(material) == 0 {
		return nil, errors.New(errors.FailedPrecondition, "key has no symmetric material")
	}

	block, err := aes.NewCipher(material)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "aes cipher: %v", err)
	}

	return cipher.NewGCM(block)
}

// requireUsable reports whether a key may be used for a cryptographic
// operation, returning the sentinel that maps to the precise KMS exception:
// Disabled → DisabledException, any other non-Enabled state → KMSInvalidState.
func requireUsable(kd *keyData) error {
	switch kd.meta.KeyState {
	case driver.StateEnabled:
		return nil
	case driver.StateDisabled:
		return driver.ErrKeyDisabled
	default:
		return driver.ErrKeyInvalidState
	}
}

// Encrypt encrypts plaintext under a key. Symmetric keys use AES-GCM; RSA keys
// use RSA-OAEP-SHA-256.
func (m *Mock) Encrypt(_ context.Context, in driver.EncryptInput) (*driver.EncryptOutput, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	if kd.meta.KeyUsage != driver.UsageEncryptDecrypt {
		return nil, driver.ErrInvalidKeyUsage
	}

	// Real KMS enforces the Plaintext length constraint server-side: a single
	// Encrypt accepts at most 4096 bytes, rejecting larger input with
	// ValidationException instead of silently sealing it.
	if len(in.Plaintext) > maxPlaintextBytes {
		return nil, errors.Newf(errors.InvalidArgument,
			"plaintext exceeds the %d-byte Encrypt limit; use GenerateDataKey for larger data", maxPlaintextBytes)
	}

	if priv, ok := kd.privKey.(*rsa.PrivateKey); ok {
		hashFn := oaepHash(in.EncryptionAlgorithm)

		ct, encErr := rsa.EncryptOAEP(hashFn(), rand.Reader, &priv.PublicKey, in.Plaintext, nil)
		if encErr != nil {
			return nil, errors.Newf(errors.InvalidArgument, "rsa encrypt: %v", encErr)
		}

		return &driver.EncryptOutput{
			KeyID:               kd.meta.KeyID,
			CiphertextBlob:      encodeBlob(blobMagicRSAOAEP, kd.meta.KeyID, ct),
			EncryptionAlgorithm: rsaAlgOrDefault(in.EncryptionAlgorithm),
		}, nil
	}

	version := len(kd.materials) - 1

	aead, err := symmetricGCM(kd.currentMaterial())
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcmNonceSize)
	if _, nerr := rand.Read(nonce); nerr != nil {
		return nil, errors.Newf(errors.Internal, "nonce: %v", nerr)
	}

	ct := aead.Seal(nil, nonce, in.Plaintext, canonicalContext(in.EncryptionContext))

	body := make([]byte, 0, keyVersionSize+len(nonce)+len(ct))
	//nolint:gosec // version is a small monotonic counter, never near uint32 max
	body = binary.BigEndian.AppendUint32(body, uint32(version))
	body = append(body, nonce...)
	body = append(body, ct...)

	return &driver.EncryptOutput{
		KeyID:               kd.meta.KeyID,
		CiphertextBlob:      encodeBlob(blobMagicGCM, kd.meta.KeyID, body),
		EncryptionAlgorithm: driver.EncSymmetricDefault,
	}, nil
}

// oaepHash picks the OAEP hash for an RSA encryption algorithm (default
// SHA-256; SHA-1 when explicitly requested).
func oaepHash(alg string) func() hash.Hash {
	if alg == driver.EncRSAOAEPSHA1 {
		return sha1.New
	}

	return sha256.New
}

func rsaAlgOrDefault(alg string) string {
	if alg == driver.EncRSAOAEPSHA1 {
		return driver.EncRSAOAEPSHA1
	}

	return driver.EncRSAOAEPSHA256
}

// Decrypt decrypts a ciphertext blob. The blob is self-describing, so KeyID is
// optional for symmetric keys but validated when supplied.
func (m *Mock) Decrypt(_ context.Context, in driver.DecryptInput) (*driver.DecryptOutput, error) {
	magic, keyID, body, err := decodeBlob(in.CiphertextBlob)
	if err != nil {
		return nil, err
	}

	if in.KeyID != "" {
		resolved, rerr := m.resolveKeyID(in.KeyID)
		if rerr != nil {
			return nil, rerr
		}

		if resolved != keyID {
			return nil, driver.ErrIncorrectKey
		}
	}

	kd, ok := m.keys.Get(keyID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "key %q not found", keyID)
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	if uerr := requireUsable(kd); uerr != nil {
		return nil, uerr
	}

	pt, alg, err := decryptBody(kd, magic, body, in.EncryptionContext)
	if err != nil {
		return nil, err
	}

	return &driver.DecryptOutput{KeyID: kd.meta.KeyID, Plaintext: pt, EncryptionAlgorithm: alg}, nil
}

func decryptBody(
	kd *keyData, magic byte, body []byte, encCtx map[string]string,
) (plaintext []byte, algorithm string, err error) {
	switch magic {
	case blobMagicRSAOAEP:
		return decryptRSA(kd, body)
	case blobMagicGCM:
		return decryptGCM(kd, body, encCtx)
	default:
		return nil, "", driver.ErrInvalidCiphertext
	}
}

func decryptRSA(kd *keyData, body []byte) (plaintext []byte, algorithm string, err error) {
	priv, ok := kd.privKey.(*rsa.PrivateKey)
	if !ok {
		return nil, "", driver.ErrIncorrectKey
	}

	// Try both OAEP hashes so a SHA-1-wrapped ciphertext still opens.
	for _, h := range []func() hash.Hash{sha256.New, sha1.New} {
		if pt, derr := rsa.DecryptOAEP(h(), rand.Reader, priv, body, nil); derr == nil {
			return pt, driver.EncRSAOAEPSHA256, nil
		}
	}

	return nil, "", driver.ErrInvalidCiphertext
}

func decryptGCM(kd *keyData, body []byte, encCtx map[string]string) (plaintext []byte, algorithm string, err error) {
	if len(body) < keyVersionSize+gcmNonceSize {
		return nil, "", driver.ErrInvalidCiphertext
	}

	version := int(binary.BigEndian.Uint32(body[:keyVersionSize]))
	if version < 0 || version >= len(kd.materials) {
		return nil, "", driver.ErrInvalidCiphertext
	}

	aead, err := symmetricGCM(kd.materials[version])
	if err != nil {
		return nil, "", err
	}

	rest := body[keyVersionSize:]
	nonce, ct := rest[:gcmNonceSize], rest[gcmNonceSize:]

	pt, err := aead.Open(nil, nonce, ct, canonicalContext(encCtx))
	if err != nil {
		return nil, "", driver.ErrInvalidCiphertext
	}

	return pt, driver.EncSymmetricDefault, nil
}

// ReEncrypt decrypts under the source key and re-encrypts under the destination.
//
//nolint:gocritic // in is the public ReEncrypt input, taken by value to match the driver API
func (m *Mock) ReEncrypt(ctx context.Context, in driver.ReEncryptInput) (*driver.ReEncryptOutput, error) {
	dec, err := m.Decrypt(ctx, driver.DecryptInput{
		KeyID:             in.SourceKeyID,
		CiphertextBlob:    in.CiphertextBlob,
		EncryptionContext: in.SourceEncryptionContext,
	})
	if err != nil {
		return nil, err
	}

	enc, err := m.Encrypt(ctx, driver.EncryptInput{
		KeyID:             in.DestinationKeyID,
		Plaintext:         dec.Plaintext,
		EncryptionContext: in.DestinationEncryptionContext,
	})
	if err != nil {
		return nil, err
	}

	return &driver.ReEncryptOutput{
		CiphertextBlob:                 enc.CiphertextBlob,
		SourceKeyID:                    dec.KeyID,
		KeyID:                          enc.KeyID,
		SourceEncryptionAlgorithm:      dec.EncryptionAlgorithm,
		DestinationEncryptionAlgorithm: enc.EncryptionAlgorithm,
	}, nil
}

func dataKeyBytes(spec string, numberOfBytes int32) (int, error) {
	switch {
	case spec == driver.DataKeyAES256:
		return aes256Bytes, nil
	case spec == driver.DataKeyAES128:
		return aes128Bytes, nil
	case spec == "" && numberOfBytes > 0 && numberOfBytes <= maxRandomBytes:
		return int(numberOfBytes), nil
	default:
		return 0, errors.New(errors.InvalidArgument, "specify KeySpec (AES_256/AES_128) or NumberOfBytes (1-1024)")
	}
}

// GenerateDataKey returns a random data key both in plaintext and encrypted
// under the KMS key.
func (m *Mock) GenerateDataKey(ctx context.Context, in driver.GenerateDataKeyInput) (*driver.GenerateDataKeyOutput, error) {
	// GenerateDataKey is only valid on symmetric keys; real KMS rejects it on
	// asymmetric keys with InvalidKeyUsageException.
	if kd, kerr := m.getKey(in.KeyID); kerr == nil {
		kd.mu.RLock()
		asym := kd.privKey != nil
		kd.mu.RUnlock()

		if asym {
			return nil, driver.ErrInvalidKeyUsage
		}
	}

	size, err := dataKeyBytes(in.KeySpec, in.NumberOfBytes)
	if err != nil {
		return nil, err
	}

	plaintext := make([]byte, size)
	if _, rerr := rand.Read(plaintext); rerr != nil {
		return nil, errors.Newf(errors.Internal, "data key: %v", rerr)
	}

	enc, err := m.Encrypt(ctx, driver.EncryptInput{
		KeyID: in.KeyID, Plaintext: plaintext, EncryptionContext: in.EncryptionContext,
	})
	if err != nil {
		return nil, err
	}

	return &driver.GenerateDataKeyOutput{
		KeyID: enc.KeyID, Plaintext: plaintext, CiphertextBlob: enc.CiphertextBlob,
	}, nil
}

// GenerateDataKeyWithoutPlaintext is GenerateDataKey with the plaintext dropped.
func (m *Mock) GenerateDataKeyWithoutPlaintext(
	ctx context.Context, in driver.GenerateDataKeyInput,
) (*driver.GenerateDataKeyOutput, error) {
	out, err := m.GenerateDataKey(ctx, in)
	if err != nil {
		return nil, err
	}

	out.Plaintext = nil

	return out, nil
}

// GenerateRandom returns cryptographically random bytes.
func (*Mock) GenerateRandom(_ context.Context, numberOfBytes int32) ([]byte, error) {
	if numberOfBytes <= 0 || numberOfBytes > maxRandomBytes {
		return nil, errors.New(errors.InvalidArgument, "NumberOfBytes must be 1-1024")
	}

	buf := make([]byte, numberOfBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, errors.Newf(errors.Internal, "random: %v", err)
	}

	return buf, nil
}
