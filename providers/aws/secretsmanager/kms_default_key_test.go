package secretsmanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestKMSKeyResolutionUnchanged guards Secrets Manager against the shared
// kmscrypto change that stops minting keys for unknown references. The default
// aws/secretsmanager key still works and is shared, an explicit real key still
// works, and an unknown explicit key is still rejected at create.
func TestKMSKeyResolutionUnchanged(t *testing.T) {
	m, k := newEncryptedMock()
	ctx := context.Background()

	for _, name := range []string{"app/one", "app/two"} {
		_, err := m.CreateSecret(ctx, driver.SecretConfig{Name: name}, []byte("v1"))
		require.NoError(t, err)

		_, err = m.PutSecretValue(ctx, name, []byte("v2"))
		require.NoError(t, err)

		got, err := m.GetSecretValue(ctx, name, "")
		require.NoError(t, err)
		assert.Equal(t, []byte("v2"), got.Value)
	}

	keys, err := k.ListKeys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "default-key secrets share one managed key")

	key, err := k.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	require.NoError(t, err)

	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "app/explicit", KMSKeyID: key.KeyID}, []byte("x"))
	require.NoError(t, err)

	_, _, err = m.UpdateSecret(ctx, "app/explicit", "", []byte("y"))
	require.NoError(t, err)

	got, err := m.GetSecretValue(ctx, "app/explicit", "")
	require.NoError(t, err)
	assert.Equal(t, []byte("y"), got.Value)

	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "app/bogus", KMSKeyID: "alias/does-not-exist"}, []byte("z"))
	assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))

	keys, err = k.ListKeys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 2, "no key minted for the unknown alias")
}
