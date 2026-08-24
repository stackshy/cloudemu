package keyvault

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRSAKeyAndCrypto(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	key, err := m.CreateKey(ctx, "k", &driver.KVCreateKeyParams{Kty: "RSA", KeySize: 2048, Attributes: driver.KVKeyAttributes{Enabled: true}})
	require.NoError(t, err)
	assert.Equal(t, "RSA", key.Kty)
	assert.NotEmpty(t, key.N)
	assert.NotEmpty(t, key.E)

	plaintext := []byte("secret payload")

	enc, err := m.EncryptKey(ctx, "k", "", driver.KVCryptoParams{Algorithm: "RSA-OAEP-256", Value: plaintext})
	require.NoError(t, err)

	dec, err := m.DecryptKey(ctx, "k", "", driver.KVCryptoParams{Algorithm: "RSA-OAEP-256", Value: enc.Value})
	require.NoError(t, err)
	assert.Equal(t, plaintext, dec.Value)
}

func TestCreateECKeySignVerify(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateKey(ctx, "ec", &driver.KVCreateKeyParams{Kty: "EC", Curve: "P-384", Attributes: driver.KVKeyAttributes{Enabled: true}})
	require.NoError(t, err)

	digest := sha256.Sum256([]byte("data"))

	sig, err := m.SignKey(ctx, "ec", "", driver.KVCryptoParams{Algorithm: "ES384", Value: digest[:]})
	require.NoError(t, err)

	ok, err := m.VerifyKey(ctx, "ec", "", driver.KVCryptoParams{Algorithm: "ES384", Value: digest[:], Signature: sig.Value})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestUnsupportedCurveRejected(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateKey(context.Background(), "k", &driver.KVCreateKeyParams{Kty: "EC", Curve: "P-256K"})
	require.Error(t, err)
}

func TestImportOctKeyAESKeyWrap(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	kek := []byte("0123456789abcdef0123456789abcdef") // 32-byte AES-256 KEK

	_, err := m.ImportKey(ctx, "kek", &driver.KVImportKeyParams{
		Key:        driver.KVImportJWK{Kty: "oct", K: kek, KeyOps: []string{"wrapKey", "unwrapKey"}},
		Attributes: driver.KVKeyAttributes{Enabled: true},
	})
	require.NoError(t, err)

	cek := []byte("fedcba9876543210fedcba9876543210") // 32-byte content key

	wrapped, err := m.WrapKey(ctx, "kek", "", driver.KVCryptoParams{Algorithm: "A256KW", Value: cek})
	require.NoError(t, err)
	assert.NotEqual(t, cek, wrapped.Value)

	unwrapped, err := m.UnwrapKey(ctx, "kek", "", driver.KVCryptoParams{Algorithm: "A256KW", Value: wrapped.Value})
	require.NoError(t, err)
	assert.Equal(t, cek, unwrapped.Value)
}

func TestAESKeyUnwrapIntegrityFailure(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	kek := []byte("0123456789abcdef") // 16-byte AES-128 KEK

	_, err := m.ImportKey(ctx, "kek", &driver.KVImportKeyParams{
		Key:        driver.KVImportJWK{Kty: "oct", K: kek},
		Attributes: driver.KVKeyAttributes{Enabled: true},
	})
	require.NoError(t, err)

	corrupt := make([]byte, 40)

	_, err = m.UnwrapKey(ctx, "kek", "", driver.KVCryptoParams{Algorithm: "A128KW", Value: corrupt})
	require.Error(t, err)
}

func TestImportRSAKeyRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Generate a key via CreateKey, then round-trip its private components back
	// through ImportKey to confirm reconstruction is exact.
	src := newTestMock()

	_, err := src.CreateKey(ctx, "orig", &driver.KVCreateKeyParams{Kty: "RSA", Attributes: driver.KVKeyAttributes{Enabled: true}})
	require.NoError(t, err)

	kd, ok := src.keys.Get("orig")
	require.True(t, ok)

	v := &kd.versions[0]
	priv := v.rsaKey

	jwk := driver.KVImportJWK{
		Kty: "RSA",
		N:   priv.N.Bytes(),
		E:   encodeExponent(priv.E),
		D:   priv.D.Bytes(),
		P:   priv.Primes[0].Bytes(),
		Q:   priv.Primes[1].Bytes(),
	}

	_, err = m.ImportKey(ctx, "imp", &driver.KVImportKeyParams{Key: jwk, Attributes: driver.KVKeyAttributes{Enabled: true}})
	require.NoError(t, err)

	digest := sha256.Sum256([]byte("cross"))

	sig, err := src.SignKey(ctx, "orig", "", driver.KVCryptoParams{Algorithm: "RS256", Value: digest[:]})
	require.NoError(t, err)

	ok2, err := m.VerifyKey(ctx, "imp", "", driver.KVCryptoParams{Algorithm: "RS256", Value: digest[:], Signature: sig.Value})
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestKeySoftDeleteRecoverPurge(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateKey(ctx, "k", &driver.KVCreateKeyParams{Kty: "RSA", Attributes: driver.KVKeyAttributes{Enabled: true}})
	require.NoError(t, err)

	_, err = m.DeleteKey(ctx, "k")
	require.NoError(t, err)

	_, err = m.GetKey(ctx, "k", "")
	require.Error(t, err)

	_, err = m.GetDeletedKey(ctx, "k")
	require.NoError(t, err)

	_, err = m.RecoverDeletedKey(ctx, "k")
	require.NoError(t, err)

	_, err = m.GetKey(ctx, "k", "")
	require.NoError(t, err)

	_, err = m.DeleteKey(ctx, "k")
	require.NoError(t, err)

	require.NoError(t, m.PurgeDeletedKey(ctx, "k"))

	_, err = m.GetDeletedKey(ctx, "k")
	require.Error(t, err)
}

func TestKeyOperationNotPermitted(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// An EC key defaults to sign/verify only; encrypt must be rejected.
	_, err := m.CreateKey(ctx, "ec", &driver.KVCreateKeyParams{Kty: "EC", Curve: "P-256", Attributes: driver.KVKeyAttributes{Enabled: true}})
	require.NoError(t, err)

	_, err = m.EncryptKey(ctx, "ec", "", driver.KVCryptoParams{Algorithm: "RSA-OAEP", Value: []byte("x")})
	require.Error(t, err)
}

func TestDisabledKeyRejectsCrypto(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateKey(ctx, "k", &driver.KVCreateKeyParams{Kty: "RSA", Attributes: driver.KVKeyAttributes{Enabled: false}})
	require.NoError(t, err)

	_, err = m.EncryptKey(ctx, "k", "", driver.KVCryptoParams{Algorithm: "RSA-OAEP", Value: []byte("x")})
	require.Error(t, err)
}
