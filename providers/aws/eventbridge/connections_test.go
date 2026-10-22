package eventbridge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/secretsmanager"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newConnectionMock returns a mock wired to a real Secrets Manager mock.
func newConnectionMock(t *testing.T) (*Mock, *secretsmanager.Mock) {
	t.Helper()

	m, fc := newTestMock()
	sm := secretsmanager.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1")))
	m.SetConnectionSecretStore(sm)

	return m, sm
}

func basicConnection(name string) *driver.ConnectionConfig {
	return &driver.ConnectionConfig{
		Name:              name,
		AuthorizationType: "BASIC",
		AuthParameters: &driver.ConnectionAuthParameters{
			Basic: &driver.ConnectionBasicAuth{Username: "user", Password: "pass"},
			InvocationHTTPParameters: &driver.ConnectionHTTPParameters{
				HeaderParameters: []driver.HTTPParameter{
					{Key: "x-plain", Value: "shown"},
					{Key: "x-secret", Value: "hidden", IsValueSecret: true},
				},
			},
		},
	}
}

func secretJSON(t *testing.T, sm *secretsmanager.Mock, arn string) map[string]any {
	t.Helper()

	name := strings.TrimSuffix(arn[strings.Index(arn, "secret:")+len("secret:"):], arn[len(arn)-7:])

	v, err := sm.GetSecretValue(context.Background(), name, "")
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(v.Value, &out))

	return out
}

func TestCreateConnectionBasic(t *testing.T) {
	m, sm := newConnectionMock(t)
	ctx := context.Background()

	c, err := m.CreateConnection(ctx, basicConnection("c1"))
	require.NoError(t, err)
	assert.Regexp(t, `^arn:aws:events:us-east-1:123456789012:connection/c1/[-0-9a-f]{36}$`, c.ARN)
	assert.Equal(t, "AUTHORIZED", c.State)
	assert.False(t, c.LastAuthorizedAt.IsZero())
	assert.Regexp(t, `^arn:aws:secretsmanager:us-east-1:123456789012:secret:events!connection/c1/[-0-9a-f]{36}-.{6}$`, c.SecretARN)

	secret := secretJSON(t, sm, c.SecretARN)
	assert.Equal(t, "user", secret["username"])
	assert.Equal(t, "pass", secret["password"])
	assert.Contains(t, secret, "invocation_http_parameters")

	got, err := m.DescribeConnection(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "user", got.AuthParameters.Basic.Username)
	assert.Empty(t, got.AuthParameters.Basic.Password, "Describe must not return the password")

	headers := got.AuthParameters.InvocationHTTPParameters.HeaderParameters
	assert.Equal(t, "shown", headers[0].Value)
	assert.Empty(t, headers[1].Value, "a secret header value must be redacted")

	_, err = m.CreateConnection(ctx, basicConnection("c1"))
	require.True(t, cerrors.IsAlreadyExists(err))
	assert.Equal(t, "Connection c1 already exists.", cerrors.Message(err))
}

func TestCreateConnectionAPIKeyAndOAuth(t *testing.T) {
	m, sm := newConnectionMock(t)
	ctx := context.Background()

	k, err := m.CreateConnection(ctx, &driver.ConnectionConfig{
		Name: "key", AuthorizationType: "API_KEY",
		AuthParameters: &driver.ConnectionAuthParameters{APIKey: &driver.ConnectionAPIKeyAuth{APIKeyName: "x-api-key", APIKeyValue: "s3cr3t"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "AUTHORIZED", k.State)
	assert.Equal(t, "s3cr3t", secretJSON(t, sm, k.SecretARN)["api_key_value"])

	o, err := m.CreateConnection(ctx, &driver.ConnectionConfig{
		Name: "oauth", AuthorizationType: "OAUTH_CLIENT_CREDENTIALS",
		AuthParameters: &driver.ConnectionAuthParameters{OAuth: &driver.ConnectionOAuth{
			AuthorizationEndpoint: "https://auth.example.com/token", HTTPMethod: "POST",
			ClientParameters: &driver.ConnectionOAuthClient{ClientID: "id", ClientSecret: "csecret"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "AUTHORIZING", o.State, "OAuth starts AUTHORIZING")
	assert.Equal(t, "csecret", secretJSON(t, sm, o.SecretARN)["client_secret"])

	got, err := m.DescribeConnection(ctx, "oauth")
	require.NoError(t, err)
	assert.Equal(t, "AUTHORIZED", got.State, "OAuth settles to AUTHORIZED on the next read")
	assert.Empty(t, got.AuthParameters.OAuth.ClientParameters.ClientSecret)
	assert.Equal(t, "id", got.AuthParameters.OAuth.ClientParameters.ClientID)
}

func TestCreateConnectionValidation(t *testing.T) {
	m, _ := newConnectionMock(t)
	ctx := context.Background()

	tests := []struct {
		name string
		cfg  *driver.ConnectionConfig
		msg  string
	}{
		{
			name: "bad name",
			cfg:  &driver.ConnectionConfig{Name: "bad name", AuthorizationType: "BASIC"},
			msg: "1 validation error detected: Value 'bad name' at 'name' failed to satisfy constraint: " +
				`Member must satisfy regular expression pattern: [\.\-_A-Za-z0-9]+`,
		},
		{
			name: "bad authorization type",
			cfg:  &driver.ConnectionConfig{Name: "c", AuthorizationType: "INVALID_AUTH_TYPE"},
			msg: "1 validation error detected: Value 'INVALID_AUTH_TYPE' at 'authorizationType' failed to satisfy " +
				"constraint: Member must satisfy enum value set: [BASIC, OAUTH_CLIENT_CREDENTIALS, API_KEY]",
		},
		{
			name: "no auth parameters",
			cfg:  &driver.ConnectionConfig{Name: "c", AuthorizationType: "BASIC"},
			msg:  "1 validation error detected: Value null at 'authParameters' failed to satisfy constraint: Member must not be null",
		},
		{
			name: "block for another type",
			cfg: &driver.ConnectionConfig{Name: "c", AuthorizationType: "BASIC", AuthParameters: &driver.ConnectionAuthParameters{
				APIKey: &driver.ConnectionAPIKeyAuth{APIKeyName: "k", APIKeyValue: "v"},
			}},
			msg: "BasicAuthParameters must be specified for BASIC authorization type.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateConnection(ctx, tc.cfg)
			require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
			assert.Equal(t, tc.msg, cerrors.Message(err))
		})
	}

	_, err := m.DescribeConnection(ctx, "c")
	assert.True(t, cerrors.IsNotFound(err), "a rejected create must not store the connection")
}

func TestUpdateConnection(t *testing.T) {
	m, sm := newConnectionMock(t)
	ctx := context.Background()

	c, err := m.CreateConnection(ctx, basicConnection("c1"))
	require.NoError(t, err)

	desc := "new"
	out, err := m.UpdateConnection(ctx, &driver.ConnectionUpdate{
		Name: "c1", Description: &desc,
		AuthParameters: &driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: "new_user"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "AUTHORIZED", out.State)

	secret := secretJSON(t, sm, c.SecretARN)
	assert.Equal(t, "new_user", secret["username"])
	assert.Equal(t, "pass", secret["password"], "an omitted password keeps its value")

	got, err := m.DescribeConnection(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "new", got.Description)

	t.Run("switching type needs the new block", func(t *testing.T) {
		typ := "API_KEY"
		_, err := m.UpdateConnection(ctx, &driver.ConnectionUpdate{Name: "c1", AuthorizationType: &typ})
		require.True(t, cerrors.IsInvalidArgument(err))

		got, err := m.DescribeConnection(ctx, "c1")
		require.NoError(t, err)
		assert.Equal(t, "BASIC", got.AuthorizationType, "a rejected update changes nothing")
	})

	t.Run("switching type with the new block", func(t *testing.T) {
		typ := "API_KEY"
		_, err := m.UpdateConnection(ctx, &driver.ConnectionUpdate{
			Name: "c1", AuthorizationType: &typ,
			AuthParameters: &driver.ConnectionAuthParameters{APIKey: &driver.ConnectionAPIKeyAuth{APIKeyName: "k", APIKeyValue: "v"}},
		})
		require.NoError(t, err)

		got, err := m.DescribeConnection(ctx, "c1")
		require.NoError(t, err)
		assert.Nil(t, got.AuthParameters.Basic, "the old BASIC block is dropped")
		assert.Equal(t, "k", got.AuthParameters.APIKey.APIKeyName)
	})

	_, err = m.UpdateConnection(ctx, &driver.ConnectionUpdate{Name: "ghost", Description: &desc})
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Connection 'ghost' does not exist.", cerrors.Message(err))
}

func TestDeauthorizeConnection(t *testing.T) {
	m, sm := newConnectionMock(t)
	ctx := context.Background()

	c, err := m.CreateConnection(ctx, basicConnection("c1"))
	require.NoError(t, err)

	out, err := m.DeauthorizeConnection(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "DEAUTHORIZING", out.State)

	got, err := m.DescribeConnection(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "DEAUTHORIZED", got.State)
	assert.Empty(t, secretJSON(t, sm, c.SecretARN)["password"], "deauthorizing removes the credential from the secret")

	_, err = m.DeauthorizeConnection(ctx, "ghost")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestDeleteConnectionDeletesSecret(t *testing.T) {
	m, sm := newConnectionMock(t)
	ctx := context.Background()

	c, err := m.CreateConnection(ctx, basicConnection("c1"))
	require.NoError(t, err)

	out, err := m.DeleteConnection(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "DELETING", out.State)

	_, err = m.DescribeConnection(ctx, "c1")
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Failed to describe the connection(s). Connection 'c1' does not exist.", cerrors.Message(err))

	secrets, err := sm.ListSecrets(ctx)
	require.NoError(t, err)

	for _, s := range secrets {
		assert.NotEqual(t, c.SecretARN, s.ResourceID, "the connection's secret must be deleted with it")
	}

	_, err = m.DeleteConnection(ctx, "c1")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestListConnections(t *testing.T) {
	m, _ := newConnectionMock(t)
	ctx := context.Background()

	for _, name := range []string{"beta", "alpha"} {
		_, err := m.CreateConnection(ctx, basicConnection(name))
		require.NoError(t, err)
	}

	all, err := m.ListConnections(ctx, driver.ConnectionFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "alpha", all[0].Name)
	assert.Empty(t, all[0].AuthParameters.Basic.Password)

	byPrefix, err := m.ListConnections(ctx, driver.ConnectionFilter{NamePrefix: "be"})
	require.NoError(t, err)
	assert.Len(t, byPrefix, 1)

	byState, err := m.ListConnections(ctx, driver.ConnectionFilter{State: "DEAUTHORIZED"})
	require.NoError(t, err)
	assert.Empty(t, byState)

	_, err = m.ListConnections(ctx, driver.ConnectionFilter{State: "BOGUS"})
	assert.True(t, cerrors.IsInvalidArgument(err))
}

// TestConnectionsConcurrent runs creates, updates, deauthorizations, reads and
// deletes at once so the race detector can check that no store lock is held
// across a Secrets Manager call.
func TestConnectionsConcurrent(t *testing.T) {
	m, sm := newConnectionMock(t)
	ctx := context.Background()

	var wg sync.WaitGroup

	for _, name := range []string{"a", "b", "c", "d"} {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = m.CreateConnection(ctx, basicConnection(name))
			desc := "x"
			_, _ = m.UpdateConnection(ctx, &driver.ConnectionUpdate{Name: name, Description: &desc})
			_, _ = m.DeauthorizeConnection(ctx, name)
			_, _ = m.DescribeConnection(ctx, name)
			_, _ = m.ListConnections(ctx, driver.ConnectionFilter{})
			_, _ = m.DeleteConnection(ctx, name)
		}()
	}

	wg.Wait()

	left, err := m.ListConnections(ctx, driver.ConnectionFilter{})
	require.NoError(t, err)
	assert.Empty(t, left)

	secrets, err := sm.ListSecrets(ctx)
	require.NoError(t, err)

	for _, s := range secrets {
		assert.NotContains(t, s.Name, "events!connection/", "every connection's secret is deleted with it")
	}
}

func TestConnectionWithoutSecretStore(t *testing.T) {
	m, _ := newTestMock()

	c, err := m.CreateConnection(context.Background(), basicConnection("c1"))
	require.NoError(t, err)
	assert.Regexp(t, `^arn:aws:secretsmanager:us-east-1:123456789012:secret:events!connection/c1/`, c.SecretARN)
}
