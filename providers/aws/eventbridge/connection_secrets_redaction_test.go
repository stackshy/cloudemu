package eventbridge

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leakyValue is an over-long credential: long enough to fail validation, and
// built from a marker that must never appear in an error message.
func leakyValue() string {
	return strings.Repeat("TOPSECRET", 60)
}

func oauthAuth(secret string) *driver.ConnectionAuthParameters {
	return &driver.ConnectionAuthParameters{OAuth: &driver.ConnectionOAuth{
		AuthorizationEndpoint: "https://auth.example.com/token", HTTPMethod: "POST",
		ClientParameters: &driver.ConnectionOAuthClient{ClientID: "id", ClientSecret: secret},
	}}
}

func withHeader(ap *driver.ConnectionAuthParameters, param driver.HTTPParameter) *driver.ConnectionAuthParameters {
	ap.InvocationHTTPParameters = &driver.ConnectionHTTPParameters{HeaderParameters: []driver.HTTPParameter{param}}

	return ap
}

// TestConnectionErrorsNeverEchoSecrets checks every sensitive member: a value
// that fails validation is reported without its content, on create and update.
func TestConnectionErrorsNeverEchoSecrets(t *testing.T) {
	secret := leakyValue()

	tests := []struct {
		name     string
		authType string
		auth     *driver.ConnectionAuthParameters
		field    string
	}{
		{
			name: "basic password", authType: "BASIC",
			auth:  &driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: "u", Password: secret}},
			field: "authParameters.basicAuthParameters.password",
		},
		{
			name: "api key value", authType: "API_KEY",
			auth:  &driver.ConnectionAuthParameters{APIKey: &driver.ConnectionAPIKeyAuth{APIKeyName: "k", APIKeyValue: secret}},
			field: "authParameters.apiKeyAuthParameters.apiKeyValue",
		},
		{
			name: "oauth client secret", authType: "OAUTH_CLIENT_CREDENTIALS",
			auth:  oauthAuth(secret),
			field: "authParameters.oAuthParameters.clientParameters.clientSecret",
		},
		{
			name: "secret header value", authType: "BASIC",
			auth: withHeader(&driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: "u", Password: "p"}},
				driver.HTTPParameter{Key: "x", Value: secret, IsValueSecret: true}),
			field: "authParameters.invocationHttpParameters.headerParameters.1.member.value",
		},
		{
			name: "plain header value", authType: "BASIC",
			auth: withHeader(&driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: "u", Password: "p"}},
				driver.HTTPParameter{Key: "x", Value: secret}),
			field: "authParameters.invocationHttpParameters.headerParameters.1.member.value",
		},
		{
			name: "oauth query parameter value", authType: "OAUTH_CLIENT_CREDENTIALS",
			auth: func() *driver.ConnectionAuthParameters {
				ap := oauthAuth("s")
				ap.OAuth.OAuthHTTPParameters = &driver.ConnectionHTTPParameters{
					QueryStringParameters: []driver.HTTPParameter{{Key: "q", Value: secret, IsValueSecret: true}},
				}

				return ap
			}(),
			field: "authParameters.oAuthParameters.oAuthHttpParameters.queryStringParameters.1.member.value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newConnectionMock(t)
			ctx := context.Background()

			_, err := m.CreateConnection(ctx, &driver.ConnectionConfig{Name: "c", AuthorizationType: tc.authType, AuthParameters: tc.auth})
			require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
			assert.NotContains(t, err.Error(), "TOPSECRET", "create error leaks the secret")
			assert.Contains(t, cerrors.Message(err), "Value at '"+tc.field+"' failed to satisfy constraint")

			// The same value sent on an update of a valid connection.
			valid := &driver.ConnectionConfig{Name: "ok", AuthorizationType: tc.authType, AuthParameters: validAuthFor(tc.authType)}
			_, err = m.CreateConnection(ctx, valid)
			require.NoError(t, err)

			_, err = m.UpdateConnection(ctx, &driver.ConnectionUpdate{Name: "ok", AuthParameters: tc.auth})
			require.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
			assert.NotContains(t, err.Error(), "TOPSECRET", "update error leaks the secret")
		})
	}
}

func validAuthFor(authType string) *driver.ConnectionAuthParameters {
	switch authType {
	case "API_KEY":
		return &driver.ConnectionAuthParameters{APIKey: &driver.ConnectionAPIKeyAuth{APIKeyName: "k", APIKeyValue: "v"}}
	case "OAUTH_CLIENT_CREDENTIALS":
		return oauthAuth("s")
	default:
		return &driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: "u", Password: "p"}}
	}
}

// TestConnectionNonSecretFieldsStillEchoed keeps AWS's usual form for members
// that are not sensitive: the offending value is quoted.
func TestConnectionNonSecretFieldsStillEchoed(t *testing.T) {
	m, _ := newConnectionMock(t)
	long := strings.Repeat("u", 513)

	_, err := m.CreateConnection(context.Background(), &driver.ConnectionConfig{
		Name: "c", AuthorizationType: "BASIC",
		AuthParameters: &driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: long, Password: "p"}},
	})
	require.True(t, cerrors.IsInvalidArgument(err))
	assert.Contains(t, cerrors.Message(err), "Value '"+long+"' at 'authParameters.basicAuthParameters.username'")
}

// TestBodyParameterValuesAreNotValidatedOrEchoed covers body parameters, which
// have no length limit: a long secret body value is accepted, never echoed, and
// redacted on Describe.
func TestBodyParameterValuesAreNotValidatedOrEchoed(t *testing.T) {
	m, _ := newConnectionMock(t)
	ctx := context.Background()

	ap := validAuthFor("BASIC")
	ap.InvocationHTTPParameters = &driver.ConnectionHTTPParameters{
		BodyParameters: []driver.HTTPParameter{{Key: "b", Value: leakyValue(), IsValueSecret: true}},
	}

	_, err := m.CreateConnection(ctx, &driver.ConnectionConfig{Name: "c", AuthorizationType: "BASIC", AuthParameters: ap})
	require.NoError(t, err)

	got, err := m.DescribeConnection(ctx, "c")
	require.NoError(t, err)
	assert.Empty(t, got.AuthParameters.InvocationHTTPParameters.BodyParameters[0].Value)
}
