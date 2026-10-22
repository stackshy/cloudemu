package eventbridge

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func destinationConfig(name, connARN string) *driver.APIDestinationConfig {
	return &driver.APIDestinationConfig{
		Name: name, ConnectionARN: connARN, InvocationEndpoint: "https://example.com/hook", HTTPMethod: "POST",
	}
}

func TestAPIDestinationLifecycle(t *testing.T) {
	m, _ := newConnectionMock(t)
	ctx := context.Background()

	c, err := m.CreateConnection(ctx, basicConnection("c1"))
	require.NoError(t, err)

	d, err := m.CreateAPIDestination(ctx, destinationConfig("d1", c.ARN))
	require.NoError(t, err)
	assert.Regexp(t, `^arn:aws:events:us-east-1:123456789012:api-destination/d1/[-0-9a-f]{36}$`, d.ARN)
	assert.Equal(t, "ACTIVE", d.State)
	assert.Equal(t, 300, d.InvocationRateLimitPerSecond, "the rate limit defaults to 300")

	_, err = m.CreateAPIDestination(ctx, destinationConfig("d1", c.ARN))
	require.True(t, cerrors.IsAlreadyExists(err))
	assert.Equal(t, "An api-destination 'd1' already exists.", cerrors.Message(err))

	method, rate, desc := "PUT", 5, "v2"
	out, err := m.UpdateAPIDestination(ctx, &driver.APIDestinationUpdate{
		Name: "d1", HTTPMethod: &method, InvocationRateLimitPerSecond: &rate, Description: &desc,
	})
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", out.State)

	got, err := m.DescribeAPIDestination(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "PUT", got.HTTPMethod)
	assert.Equal(t, 5, got.InvocationRateLimitPerSecond)
	assert.Equal(t, "v2", got.Description)

	list, err := m.ListAPIDestinations(ctx, driver.APIDestinationFilter{ConnectionARN: c.ARN})
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, m.DeleteAPIDestination(ctx, "d1"))

	_, err = m.DescribeAPIDestination(ctx, "d1")
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Failed to describe the api-destination(s). An api-destination 'd1' does not exist.", cerrors.Message(err))

	err = m.DeleteAPIDestination(ctx, "d1")
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "An api-destination 'd1' does not exist.", cerrors.Message(err))
}

func TestAPIDestinationStateFollowsConnection(t *testing.T) {
	m, _ := newConnectionMock(t)
	ctx := context.Background()

	o, err := m.CreateConnection(ctx, &driver.ConnectionConfig{
		Name: "oauth", AuthorizationType: "OAUTH_CLIENT_CREDENTIALS",
		AuthParameters: &driver.ConnectionAuthParameters{OAuth: &driver.ConnectionOAuth{
			AuthorizationEndpoint: "https://auth.example.com/token", HTTPMethod: "POST",
			ClientParameters: &driver.ConnectionOAuthClient{ClientID: "id", ClientSecret: "s"},
		}},
	})
	require.NoError(t, err)

	d, err := m.CreateAPIDestination(ctx, destinationConfig("d", o.ARN))
	require.NoError(t, err)
	assert.Equal(t, "INACTIVE", d.State, "a destination on an AUTHORIZING connection starts INACTIVE")

	got, err := m.DescribeAPIDestination(ctx, "d")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", got.State)

	_, err = m.DeauthorizeConnection(ctx, "oauth")
	require.NoError(t, err)

	got, err = m.DescribeAPIDestination(ctx, "d")
	require.NoError(t, err)
	assert.Equal(t, "INACTIVE", got.State, "a deauthorized connection makes the destination INACTIVE")

	_, err = m.DeleteConnection(ctx, "oauth")
	require.NoError(t, err)

	got, err = m.DescribeAPIDestination(ctx, "d")
	require.NoError(t, err)
	assert.Equal(t, "INACTIVE", got.State)
}

func TestCreateAPIDestinationValidation(t *testing.T) {
	m, _ := newConnectionMock(t)
	ctx := context.Background()

	_, err := m.CreateAPIDestination(ctx, &driver.APIDestinationConfig{
		Name: "d", ConnectionARN: "invalid-connection-arn", InvocationEndpoint: "https://example.com", HTTPMethod: "INVALID_METHOD",
	})
	require.True(t, cerrors.IsInvalidArgument(err))
	assert.Equal(t, "2 validation errors detected: Value 'invalid-connection-arn' at 'connectionArn' failed to satisfy constraint: "+
		"Member must satisfy regular expression pattern: "+connectionARNPattern+"; Value 'INVALID_METHOD' at 'httpMethod' failed to "+
		"satisfy constraint: Member must satisfy enum value set: [POST, GET, HEAD, OPTIONS, PUT, PATCH, DELETE]", cerrors.Message(err))

	missing := "arn:aws:events:us-east-1:123456789012:connection/ghost/0b8c0e5c-5c3f-4c0e-9d1e-3f6a2b1c9d8e"
	_, err = m.CreateAPIDestination(ctx, destinationConfig("d", missing))
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Connection 'ghost' does not exist.", cerrors.Message(err))

	zero := 0
	cfg := destinationConfig("d", missing)
	cfg.InvocationRateLimitPerSecond = &zero

	_, err = m.CreateAPIDestination(ctx, cfg)
	require.True(t, cerrors.IsInvalidArgument(err))
}
