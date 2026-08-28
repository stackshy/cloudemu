// Package kmscrypto routes Secrets Manager and SSM SecureString values through
// real KMS envelope encryption so their at-rest form is genuine ciphertext and
// their KMS-failure paths are real: a disabled or deleted key makes a read fail
// exactly as it does in AWS.
//
// Encrypt asks KMS for a data key (wrapped under the addressed KMS key), seals
// the value locally with AES-256-GCM under that data key, and returns a
// self-describing blob carrying the wrapped key. Decrypt unwraps the data key
// back through KMS — the step that surfaces a disabled/deleted key — then opens
// the AES-GCM ciphertext. KMS's own crypto (providers/aws/kms) is used as-is and
// never modified.
package kmscrypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"sync"

	"github.com/stackshy/cloudemu/v2/errors"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// Envelope blob layout: magic(1) | wrappedKeyLen(uint32 BE) | wrappedKey |
// nonce(12) | AES-256-GCM ciphertext. The wrapped key is a KMS ciphertext blob
// that names its own key, so Decrypt needs no key id from the caller.
const (
	blobMagic  = 0x01
	nonceSize  = 12
	headerSize = 5 // magic(1) + wrappedKeyLen(4)
)

// KMS is the slice of the KMS backend this package needs. The KMS mock
// (*kms.Mock) satisfies it; a data key is minted per value and unwrapped on
// read, and an unresolvable managed-key reference is created on demand.
type KMS interface {
	DescribeKey(ctx context.Context, keyID string) (*kmsdriver.KeyMetadata, error)
	CreateKey(ctx context.Context, in kmsdriver.CreateKeyInput) (*kmsdriver.KeyMetadata, error)
	GenerateDataKey(ctx context.Context, in kmsdriver.GenerateDataKeyInput) (*kmsdriver.GenerateDataKeyOutput, error)
	Decrypt(ctx context.Context, in kmsdriver.DecryptInput) (*kmsdriver.DecryptOutput, error)
}

// Envelope encrypts and decrypts secret values through a KMS backend.
type Envelope struct {
	kms KMS

	mu sync.Mutex
	// managed caches a key reference (typically the reserved
	// alias/aws/secretsmanager or alias/aws/ssm managed-key aliases, which KMS
	// won't let callers create) to the customer-managed key created on demand for
	// it, so every value under one reference shares a single key.
	managed map[string]string
}

// New wraps a KMS backend as an Envelope encryptor.
func New(k KMS) *Envelope {
	return &Envelope{kms: k, managed: make(map[string]string)}
}

// DescribeKey passes through to KMS so a caller can validate that an explicit
// KmsKeyId names a real key before adopting it.
func (e *Envelope) DescribeKey(ctx context.Context, keyID string) (*kmsdriver.KeyMetadata, error) {
	return e.kms.DescribeKey(ctx, keyID)
}

// resolveKeyID turns a key reference (key id, ARN, or alias) into a usable key
// id. A reference KMS already resolves is used directly; one it does not (the
// reserved AWS-managed aliases, or an SSM key id that was never validated) gets
// a customer-managed key created and cached on demand.
func (e *Envelope) resolveKeyID(ctx context.Context, keyRef string) (string, error) {
	if md, err := e.kms.DescribeKey(ctx, keyRef); err == nil {
		return md.KeyID, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if id, ok := e.managed[keyRef]; ok {
		if _, err := e.kms.DescribeKey(ctx, id); err == nil {
			return id, nil
		}
	}

	md, err := e.kms.CreateKey(ctx, kmsdriver.CreateKeyInput{Description: "cloudemu managed key for " + keyRef})
	if err != nil {
		return "", err
	}

	e.managed[keyRef] = md.KeyID

	return md.KeyID, nil
}

// Encrypt seals plaintext under the key named by keyRef and returns the envelope
// blob. keyRef must be non-empty (callers pass an explicit KmsKeyId or their
// service's default managed alias).
func (e *Envelope) Encrypt(ctx context.Context, keyRef string, plaintext []byte) ([]byte, error) {
	keyID, err := e.resolveKeyID(ctx, keyRef)
	if err != nil {
		return nil, err
	}

	dk, err := e.kms.GenerateDataKey(ctx, kmsdriver.GenerateDataKeyInput{KeyID: keyID, KeySpec: kmsdriver.DataKeyAES256})
	if err != nil {
		return nil, err
	}

	defer wipe(dk.Plaintext)

	aead, err := gcm(dk.Plaintext)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceSize)
	if _, rerr := rand.Read(nonce); rerr != nil {
		return nil, errors.Newf(errors.Internal, "nonce: %v", rerr)
	}

	sealed := aead.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, headerSize+len(dk.CiphertextBlob)+len(nonce)+len(sealed))
	out = append(out, blobMagic)
	//nolint:gosec // a KMS ciphertext blob is a few hundred bytes, never near uint32 max
	out = binary.BigEndian.AppendUint32(out, uint32(len(dk.CiphertextBlob)))
	out = append(out, dk.CiphertextBlob...)
	out = append(out, nonce...)
	out = append(out, sealed...)

	return out, nil
}

// Decrypt opens an envelope blob produced by Encrypt. Unwrapping the data key
// goes back through KMS, so a disabled or deleted key surfaces the KMS error
// here — matching a real Secrets Manager / SSM read against a broken key.
func (e *Envelope) Decrypt(ctx context.Context, blob []byte) ([]byte, error) {
	if len(blob) < headerSize || blob[0] != blobMagic {
		return nil, errInvalidBlob()
	}

	wrappedLen := int(binary.BigEndian.Uint32(blob[1:headerSize]))

	rest := blob[headerSize:]
	if len(rest) < wrappedLen+nonceSize {
		return nil, errInvalidBlob()
	}

	wrapped := rest[:wrappedLen]
	rest = rest[wrappedLen:]
	nonce, sealed := rest[:nonceSize], rest[nonceSize:]

	dec, err := e.kms.Decrypt(ctx, kmsdriver.DecryptInput{CiphertextBlob: wrapped})
	if err != nil {
		return nil, err
	}

	defer wipe(dec.Plaintext)

	aead, err := gcm(dec.Plaintext)
	if err != nil {
		return nil, err
	}

	pt, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, errInvalidBlob()
	}

	return pt, nil
}

// errInvalidBlob is the error for a malformed, truncated, or tampered blob.
func errInvalidBlob() error {
	return errors.New(errors.InvalidArgument, "invalid encrypted value")
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "aes cipher: %v", err)
	}

	return cipher.NewGCM(block)
}

// wipe zeroes plaintext key material after use.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
