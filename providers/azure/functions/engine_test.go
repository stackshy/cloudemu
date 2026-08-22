package functions

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEngine is an in-process config.FunctionEngine that records deployments and
// returns canned results, so the provider wiring can be exercised without
// spawning a real runtime (no Docker).
type fakeEngine struct {
	deployed map[string][]byte
	removed  []string
	invoked  []string
	result   config.FunctionResult
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{deployed: map[string][]byte{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract.
func (f *fakeEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	f.deployed[fn.Name] = fn.Code

	return nil
}

func (f *fakeEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	f.invoked = append(f.invoked, name)

	return f.result, nil
}

func (f *fakeEngine) Remove(_ context.Context, name string) error {
	f.removed = append(f.removed, name)

	return nil
}

func newEngineMock(eng config.FunctionEngine) *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"),
		config.WithFunctionEngine(eng)))
}

func engineFuncConfig() driver.FunctionConfig {
	return driver.FunctionConfig{
		Name:    "real-fn",
		Runtime: "python|3.12",
		Handler: "function_app.main",
		Code:    []byte("zip-bytes"),
	}
}

func TestZipDeployToEngineOnCreate(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{Payload: []byte(`{"ok":true}`)}
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	require.NoError(t, err)
	assert.Equal(t, "zip-bytes", string(eng.deployed["real-fn"]))
}

func TestInvokeUsesEngineWhenCodeDeployed(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{Payload: []byte(`{"ok":true}`)}
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	require.NoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn", Payload: []byte(`{}`)})
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(out.Payload))
	assert.Len(t, eng.invoked, 1)
}

func TestInvokeEngineFunctionError(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{FunctionError: "ValueError: boom"}
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	require.NoError(t, err)

	// A handler that raised is surfaced via Error (HTTP stays 200) so the wire
	// handler can render Azure's 500 + plain-text error convention.
	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
	require.NoError(t, err)
	assert.Equal(t, "ValueError: boom", out.Error)
	assert.Equal(t, 200, out.StatusCode)
}

func TestRegisteredHandlerBeatsEngine(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{Payload: []byte("from-engine")}
	m := newEngineMock(eng)
	ctx := context.Background()

	m.RegisterHandler("real-fn", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("from-handler"), nil
	})

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	require.NoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
	require.NoError(t, err)
	assert.Equal(t, "from-handler", string(out.Payload))
	assert.Empty(t, eng.invoked)
}

func TestUpdateRedeploysCode(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	require.NoError(t, err)
	assert.Equal(t, "zip-bytes", string(eng.deployed["real-fn"]))

	// A code-only update (the zipdeploy shape) re-deploys the new package.
	_, err = m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Name: "real-fn", Code: []byte("new-zip")})
	require.NoError(t, err)
	assert.Equal(t, "new-zip", string(eng.deployed["real-fn"]))
}

func TestDeleteRemovesFromEngine(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	require.NoError(t, err)

	require.NoError(t, m.DeleteFunction(ctx, "real-fn"))
	assert.Equal(t, []string{"real-fn"}, eng.removed)
}

func TestNoCodeStaysStub(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	cfg := engineFuncConfig()
	cfg.Code = nil

	_, err := m.CreateFunction(ctx, cfg)
	require.NoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn", Payload: []byte(`{"a":1}`)})
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(out.Payload)) // echo stub, engine untouched
	assert.Empty(t, eng.invoked)
	assert.Empty(t, eng.deployed)
}
