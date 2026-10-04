package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestCreateAccessKeyAuthTokenIsRandom(t *testing.T) {
	m := newMock(t)
	ctx := t.Context()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "erin"})
	require.NoError(t, err)

	first, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "erin"})
	require.NoError(t, err)

	second, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "erin"})
	require.NoError(t, err)

	for _, tok := range []string{first.SecretAccessKey, second.SecretAccessKey} {
		assert.Len(t, tok, 20)
		assert.False(t, strings.HasPrefix(tok, "authtoken-"), "token %q is counter-derived", tok)
	}

	assert.NotEqual(t, first.SecretAccessKey, second.SecretAccessKey)
}
