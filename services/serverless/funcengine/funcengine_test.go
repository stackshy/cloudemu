package funcengine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

type stubEngine struct {
	deploy  config.FunctionDeployment
	result  config.FunctionResult
	invErr  error
	removed string
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (s *stubEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	s.deploy = fn

	return nil
}

func (s *stubEngine) Invoke(_ context.Context, _ string, _ []byte) (config.FunctionResult, error) {
	return s.result, s.invErr
}

func (s *stubEngine) Remove(_ context.Context, name string) error {
	s.removed = name

	return nil
}

func TestDeployNilEngineIsNotBacked(t *testing.T) {
	backed, err := Deploy(context.Background(), nil, &driver.FunctionConfig{Name: "f", Code: []byte("z")})
	require.NoError(t, err)
	assert.False(t, backed)
}

func TestDeployNoCodeIsNotBacked(t *testing.T) {
	backed, err := Deploy(context.Background(), &stubEngine{}, &driver.FunctionConfig{Name: "f"})
	require.NoError(t, err)
	assert.False(t, backed)
}

func TestDeployPassesFields(t *testing.T) {
	eng := &stubEngine{}
	cfg := &driver.FunctionConfig{
		Name: "f", Runtime: "python3.12", Handler: "m.h",
		Code: []byte("zip"), Environment: map[string]string{"K": "V"}, Timeout: 5,
	}

	backed, err := Deploy(context.Background(), eng, cfg)
	require.NoError(t, err)
	assert.True(t, backed)
	assert.Equal(t, "python3.12", eng.deploy.Runtime)
	assert.Equal(t, "m.h", eng.deploy.Handler)
	assert.Equal(t, map[string]string{"K": "V"}, eng.deploy.Env)
	assert.Equal(t, 5, eng.deploy.Timeout)
}

func TestInvokeMapsPayload(t *testing.T) {
	eng := &stubEngine{result: config.FunctionResult{Payload: []byte(`{"ok":1}`)}}

	out, err := Invoke(context.Background(), eng, "f", []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, okStatusCode, out.StatusCode)
	assert.Equal(t, `{"ok":1}`, string(out.Payload))
	assert.Empty(t, out.Error)
}

func TestInvokeMapsFunctionError(t *testing.T) {
	eng := &stubEngine{result: config.FunctionResult{FunctionError: "boom"}}

	out, err := Invoke(context.Background(), eng, "f", nil)
	require.NoError(t, err)
	assert.Equal(t, "boom", out.Error)
}

func TestInvokeEngineError(t *testing.T) {
	eng := &stubEngine{invErr: errors.New("runtime down")}

	_, err := Invoke(context.Background(), eng, "f", nil)
	assert.Error(t, err)
}

func TestRemoveNilEngine(t *testing.T) {
	assert.NoError(t, Remove(context.Background(), nil, "f"))
}

func TestRemoveDelegates(t *testing.T) {
	eng := &stubEngine{}
	require.NoError(t, Remove(context.Background(), eng, "f"))
	assert.Equal(t, "f", eng.removed)
}
