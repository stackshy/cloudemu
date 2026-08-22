package lambda

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// fakeEngine is an in-process config.FunctionEngine that records deployments and
// returns canned results, so the provider wiring can be tested without spawning
// a real runtime.
type fakeEngine struct {
	deployed map[string][]byte
	removed  []string
	result   config.FunctionResult
	invoked  []string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{deployed: map[string][]byte{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
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
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithFunctionEngine(eng)))
}

func engineFuncConfig() driver.FunctionConfig {
	return driver.FunctionConfig{
		Name:    "real-fn",
		Runtime: "python3.12",
		Handler: "main.handler",
		Code:    []byte("zip-bytes"),
	}
}

func TestInvokeUsesEngineWhenCodeDeployed(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{Payload: []byte(`{"ok":true}`)}
	m := newEngineMock(eng)
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if got := string(eng.deployed["real-fn"]); got != "zip-bytes" {
		t.Fatalf("code not deployed to engine: %q", got)
	}

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn", Payload: []byte(`{}`)})
	requireNoError(t, err)
	assertEqual(t, `{"ok":true}`, string(out.Payload))
	assertEqual(t, 1, len(eng.invoked))
}

func TestInvokeEngineFunctionError(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{FunctionError: "ValueError: boom"}
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	requireNoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
	requireNoError(t, err)
	assertEqual(t, "ValueError: boom", out.Error)
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
	requireNoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
	requireNoError(t, err)
	assertEqual(t, "from-handler", string(out.Payload))
	assertEqual(t, 0, len(eng.invoked))
}

func TestUpdateRedeploysCode(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	requireNoError(t, err)
	assertEqual(t, "zip-bytes", string(eng.deployed["real-fn"]))

	cfg := engineFuncConfig()
	cfg.Code = []byte("new-zip")

	_, err = m.UpdateFunction(ctx, "real-fn", cfg)
	requireNoError(t, err)
	assertEqual(t, "new-zip", string(eng.deployed["real-fn"]))
}

func TestDeleteRemovesFromEngine(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, engineFuncConfig())
	requireNoError(t, err)

	requireNoError(t, m.DeleteFunction(ctx, "real-fn"))
	assertEqual(t, 1, len(eng.removed))
	assertEqual(t, "real-fn", eng.removed[0])
}

func TestNoCodeStaysStub(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	cfg := engineFuncConfig()
	cfg.Code = nil

	_, err := m.CreateFunction(ctx, cfg)
	requireNoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn", Payload: []byte(`{"a":1}`)})
	requireNoError(t, err)
	assertEqual(t, `{"a":1}`, string(out.Payload)) // echo stub, engine untouched
	assertEqual(t, 0, len(eng.invoked))
}
