package secretsmanager

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/providers/aws/kmscrypto"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEncryptedMock() (*Mock, *kms.Mock) {
	k := kms.New(config.NewOptions(config.WithRegion("us-east-1")))
	m := newTestMock()
	m.SetKMSCrypto(kmscrypto.New(k))

	return m, k
}

// storedValue reaches into the backing store to read a version's at-rest bytes,
// so a test can assert the value is stored as ciphertext rather than plaintext.
func storedValue(t *testing.T, m *Mock, name string) []byte {
	t.Helper()

	sd, ok := m.secrets.Get(name)
	require.True(t, ok, "secret %q not found", name)

	require.NotEmpty(t, sd.versions)

	return sd.versions[len(sd.versions)-1].Value
}

func TestSecretEncryptedAtRestAndRoundTrips(t *testing.T) {
	m, _ := newEncryptedMock()
	ctx := context.Background()

	const value = "hunter2"

	_, err := m.CreateSecret(ctx, driver.SecretConfig{Name: "db/creds"}, []byte(value))
	require.NoError(t, err)

	// At rest, the value is genuine ciphertext, not the plaintext.
	assert.NotEqual(t, []byte(value), storedValue(t, m, "db/creds"))

	// GetSecretValue decrypts back to the original plaintext.
	got, err := m.GetSecretValue(ctx, "db/creds", "")
	require.NoError(t, err)
	assert.Equal(t, value, string(got.Value))

	// A new version through PutSecretValue also round-trips.
	_, err = m.PutSecretValue(ctx, "db/creds", []byte("rotated"))
	require.NoError(t, err)

	got, err = m.GetSecretValue(ctx, "db/creds", "")
	require.NoError(t, err)
	assert.Equal(t, "rotated", string(got.Value))
}

func TestGetSecretValueFailsAfterKeyDisabled(t *testing.T) {
	m, k := newEncryptedMock()
	ctx := context.Background()

	key, err := k.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	require.NoError(t, err)

	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "db/creds", KMSKeyID: key.KeyID}, []byte("v"))
	require.NoError(t, err)

	// Readable while the key is enabled.
	_, err = m.GetSecretValue(ctx, "db/creds", "")
	require.NoError(t, err)

	require.NoError(t, k.DisableKey(ctx, key.KeyID))

	// A disabled key makes the read fail, as in real Secrets Manager.
	_, err = m.GetSecretValue(ctx, "db/creds", "")
	require.Error(t, err)
}

func TestSecretNoKMSFallbackStoresPlaintext(t *testing.T) {
	m := newTestMock() // no KMS wired
	ctx := context.Background()

	_, err := m.CreateSecret(ctx, driver.SecretConfig{Name: "db/creds"}, []byte("plain"))
	require.NoError(t, err)

	assert.Equal(t, []byte("plain"), storedValue(t, m, "db/creds"))

	got, err := m.GetSecretValue(ctx, "db/creds", "")
	require.NoError(t, err)
	assert.Equal(t, "plain", string(got.Value))
}
