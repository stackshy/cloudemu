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
	"crypto/sha256"
	"encoding/binary"
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
// magic 0x01 = AES-256-GCM (body = nonce(12) | ciphertext); the encryption
// context is bound as AES-GCM additional data. magic 0x02 = RSA-OAEP-SHA-256
// (body = RSA ciphertext).
const (
	blobMagicGCM     = 0x01
	blobMagicRSAOAEP = 0x02
	gcmNonceSize     = 12
	rsa2048Bits      = 2048
	rsa3072Bits      = 3072
	rsa4096Bits      = 4096
	aes256Bytes      = 32
	aes128Bytes      = 16
	maxRandomBytes   = 1024
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

// canonicalContext renders the encryption context as sorted key=value pairs so
// it can be bound as deterministic AEAD additional data.
func canonicalContext(ctx map[string]string) []byte {
	if len(ctx) == 0 {
		return nil
	}

	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(ctx[k])
		b.WriteByte(';')
	}

	return []byte(b.String())
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
		return 0, "", nil, errors.New(errors.InvalidArgument, "malformed ciphertext blob")
	}

	magic = blob[0]
	idLen := int(binary.BigEndian.Uint16(blob[1:header]))

	if len(blob) < header+idLen {
		return 0, "", nil, errors.New(errors.InvalidArgument, "malformed ciphertext blob")
	}

	keyID = string(blob[header : header+idLen])
	body = blob[header+idLen:]

	return magic, keyID, body, nil
}

// symmetricGCM builds an AES-GCM AEAD from a key's material.
func symmetricGCM(kd *keyData) (cipher.AEAD, error) {
	if len(kd.material) == 0 {
		return nil, errors.New(errors.FailedPrecondition, "key has no symmetric material")
	}

	block, err := aes.NewCipher(kd.material)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "aes cipher: %v", err)
	}

	return cipher.NewGCM(block)
}

func requireUsable(kd *keyData) error {
	if kd.meta.KeyState != driver.StateEnabled {
		return errors.Newf(errors.FailedPrecondition, "key %q is not enabled (state %s)", kd.meta.KeyID, kd.meta.KeyState)
	}

	return nil
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
		return nil, errors.Newf(errors.InvalidArgument, "key %q does not support ENCRYPT_DECRYPT", kd.meta.KeyID)
	}

	if priv, ok := kd.privKey.(*rsa.PrivateKey); ok {
		ct, encErr := rsa.EncryptOAEP(sha256.New(), rand.Reader, &priv.PublicKey, in.Plaintext, nil)
		if encErr != nil {
			return nil, errors.Newf(errors.InvalidArgument, "rsa encrypt: %v", encErr)
		}

		return &driver.EncryptOutput{
			KeyID:               kd.meta.KeyID,
			CiphertextBlob:      encodeBlob(blobMagicRSAOAEP, kd.meta.KeyID, ct),
			EncryptionAlgorithm: driver.EncRSAOAEPSHA256,
		}, nil
	}

	aead, err := symmetricGCM(kd)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcmNonceSize)
	if _, nerr := rand.Read(nonce); nerr != nil {
		return nil, errors.Newf(errors.Internal, "nonce: %v", nerr)
	}

	ct := aead.Seal(nil, nonce, in.Plaintext, canonicalContext(in.EncryptionContext))
	body := append(nonce, ct...) //nolint:gocritic // nonce is a fresh slice; intentional new blob body

	return &driver.EncryptOutput{
		KeyID:               kd.meta.KeyID,
		CiphertextBlob:      encodeBlob(blobMagicGCM, kd.meta.KeyID, body),
		EncryptionAlgorithm: driver.EncSymmetricDefault,
	}, nil
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
			return nil, errors.New(errors.InvalidArgument, "ciphertext was not encrypted under the supplied key")
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
		priv, ok := kd.privKey.(*rsa.PrivateKey)
		if !ok {
			return nil, "", errors.New(errors.InvalidArgument, "key cannot decrypt RSA ciphertext")
		}

		pt, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, body, nil)
		if err != nil {
			return nil, "", errors.Newf(errors.InvalidArgument, "rsa decrypt: %v", err)
		}

		return pt, driver.EncRSAOAEPSHA256, nil
	case blobMagicGCM:
		aead, err := symmetricGCM(kd)
		if err != nil {
			return nil, "", err
		}

		if len(body) < gcmNonceSize {
			return nil, "", errors.New(errors.InvalidArgument, "malformed ciphertext blob")
		}

		nonce, ct := body[:gcmNonceSize], body[gcmNonceSize:]

		pt, err := aead.Open(nil, nonce, ct, canonicalContext(encCtx))
		if err != nil {
			return nil, "", errors.New(errors.InvalidArgument, "decryption failed (wrong key or encryption context)")
		}

		return pt, driver.EncSymmetricDefault, nil
	default:
		return nil, "", errors.New(errors.InvalidArgument, "unknown ciphertext format")
	}
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
