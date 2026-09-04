package cloudfunctions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// fakeEngine is an in-process config.FunctionEngine for testing the provider
// wiring without spawning a real runtime.
type fakeEngine struct {
	deployed map[string][]byte
	frames   map[string]string
	removed  []string
	result   config.FunctionResult
	invoked  []string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{deployed: map[string][]byte{}, frames: map[string]string{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (f *fakeEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	f.deployed[fn.Name] = fn.Code
	f.frames[fn.Name] = fn.Framework

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
		Name: "real-fn", Runtime: "python312", Handler: "hello_http",
		Code: []byte("zip-bytes"), Framework: "http",
	}
}

func TestGCPInvokeUsesEngineWhenCodeDeployed(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{Payload: []byte(`{"doubled":42}`)}
	m := newEngineMock(eng)
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if got := string(eng.deployed["real-fn"]); got != "zip-bytes" {
		t.Fatalf("code not deployed: %q", got)
	}

	if eng.frames["real-fn"] != "http" {
		t.Fatalf("framework not propagated: %q", eng.frames["real-fn"])
	}

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn", Payload: []byte(`{"n":21}`)})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if string(out.Payload) != `{"doubled":42}` || len(eng.invoked) != 1 {
		t.Fatalf("engine not used: payload=%q invoked=%d", out.Payload, len(eng.invoked))
	}
}

func TestGCPInvokeEngineFunctionError(t *testing.T) {
	eng := newFakeEngine()
	eng.result = config.FunctionResult{FunctionError: "ValueError: boom"}
	m := newEngineMock(eng)
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if out.Error != "ValueError: boom" {
		t.Fatalf("want function error, got %q", out.Error)
	}
}

func TestGCPUpdateRedeploysCode(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cfg := engineFuncConfig()
	cfg.Code = []byte("new-zip")

	if _, err := m.UpdateFunction(ctx, "real-fn", cfg); err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}

	if got := string(eng.deployed["real-fn"]); got != "new-zip" {
		t.Fatalf("code not redeployed: %q", got)
	}
}

func TestGCPDeleteRemovesFromEngine(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if err := m.DeleteFunction(ctx, "real-fn"); err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}

	if len(eng.removed) != 1 || eng.removed[0] != "real-fn" {
		t.Fatalf("engine.Remove not called: %v", eng.removed)
	}
}

// concurrentEngine is a thread-safe config.FunctionEngine that tracks whether
// a name is currently live, so a -race test can assert that a losing
// CreateFunction racer never tears down the winner's deployment. funcengine's
// Deploy/Remove are keyed by name only, so a naive "clean up my own
// deployment" Remove(name) call by a losing racer removes whatever is
// currently registered under that name — which can be the winner's.
type concurrentEngine struct {
	mu   sync.Mutex
	live map[string]bool
}

func newConcurrentEngine() *concurrentEngine {
	return &concurrentEngine{live: map[string]bool{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (e *concurrentEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.live[fn.Name] = true

	return nil
}

func (e *concurrentEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.live[name] {
		return config.FunctionResult{}, errors.New("not deployed")
	}

	return config.FunctionResult{Payload: []byte(`{"ok":true}`)}, nil
}

func (e *concurrentEngine) Remove(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.live[name] = false

	return nil
}

// TestGCPConcurrentCreateSameNameKeepsWinnerInvokable races many CreateFunction
// calls for the same name against each other with a real FunctionEngine
// configured. Exactly one must win (AlreadyExists for the rest), and — the
// case this test exists for — the winner's engine deployment must survive:
// a losing racer must never remove it out from under the winner. Run under
// -race.
func TestGCPConcurrentCreateSameNameKeepsWinnerInvokable(t *testing.T) {
	eng := newConcurrentEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	const iterations = 20
	const racers = 10

	for iter := 0; iter < iterations; iter++ {
		var wg sync.WaitGroup

		results := make([]error, racers)

		for i := 0; i < racers; i++ {
			wg.Add(1)

			go func(idx int) {
				defer wg.Done()

				_, err := m.CreateFunction(ctx, engineFuncConfig())
				results[idx] = err
			}(i)
		}

		wg.Wait()

		winners := 0

		for _, err := range results {
			if err == nil {
				winners++
			}
		}

		if winners != 1 {
			t.Fatalf("iteration %d: want exactly 1 winning create, got %d", iter, winners)
		}

		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
		if err != nil {
			t.Fatalf("iteration %d: Invoke after concurrent create: %v", iter, err)
		}

		if out.Error != "" {
			t.Fatalf("iteration %d: winner's deployment was torn down by a losing racer: %q", iter, out.Error)
		}

		if err := m.DeleteFunction(ctx, "real-fn"); err != nil {
			t.Fatalf("iteration %d: DeleteFunction: %v", iter, err)
		}
	}
}

func TestGCPNoCodeStaysStub(t *testing.T) {
	eng := newFakeEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	cfg := engineFuncConfig()
	cfg.Code = nil

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn", Payload: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if string(out.Payload) != `{"a":1}` || len(eng.invoked) != 0 {
		t.Fatalf("expected echo stub, got payload=%q invoked=%d", out.Payload, len(eng.invoked))
	}
}
