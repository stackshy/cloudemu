package lambda

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestConcurrentInvokeDuringCodeUpdatesNeverHitsRemovedDeployment: under
// AsyncSettle, invocations of $LATEST that race code updates must always find
// the deployment they picked. A code update retires and removes the deployment
// from two updates back, which an invocation holding an older copy of the
// function, or one still running, may have picked.
func TestConcurrentInvokeDuringCodeUpdatesNeverHitsRemovedDeployment(t *testing.T) {
	ctx := context.Background()
	eng := newCodeEngine()
	m, fc := newEngineSettleMock(eng, true)

	cfg := engineFuncConfig()
	cfg.Code = []byte("v0")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	const (
		invokers = 4
		updates  = 1500
	)

	var (
		failures atomic.Int64
		firstErr atomic.Value
		wg       sync.WaitGroup
	)

	stop := make(chan struct{})

	for range invokers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				if _, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"}); err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				}
			}
		}()
	}

	for i := range updates {
		if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: fmt.Appendf(nil, "v%d", i+1)}); err != nil {
			t.Errorf("UpdateFunction %d: %v", i+1, err)

			break
		}

		fc.Advance(2 * time.Second)
	}

	close(stop)
	wg.Wait()

	if n := failures.Load(); n > 0 {
		t.Fatalf("%d invocations failed during code updates; first: %v", n, firstErr.Load())
	}

	// Only the live deployment and the prior revision's remain.
	if keys := eng.keys(); len(keys) > 2 {
		t.Errorf("engine holds %d deployments after the updates, want at most 2: %v", len(keys), keys)
	}
}

// blockingEngine is a codeEngine whose Invoke of the key in hold waits until
// release is closed and only then looks the deployment up, like an engine whose
// invocation is still running when the deployment is torn down.
type blockingEngine struct {
	*codeEngine

	hold    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *blockingEngine) Invoke(ctx context.Context, name string, event []byte) (config.FunctionResult, error) {
	if name == e.hold {
		e.once.Do(func() { close(e.entered) })
		<-e.release
	}

	return e.codeEngine.Invoke(ctx, name, event)
}

// TestCodeUpdateWaitsForRunningInvocationOfRetiredDeployment: an invocation
// still running on the prior revision's deployment when the next code update
// retires it completes normally; the update removes that deployment only after
// the invocation finishes.
func TestCodeUpdateWaitsForRunningInvocationOfRetiredDeployment(t *testing.T) {
	ctx := context.Background()
	eng := &blockingEngine{
		codeEngine: newCodeEngine(), hold: "real-fn",
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	m, fc := newEngineSettleMock(eng, true)

	cfg := engineFuncConfig()
	cfg.Code = []byte("v1")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")}); err != nil {
		t.Fatal(err)
	}

	// The update is InProgress, so this invocation runs v1 under "real-fn".
	type result struct {
		out *driver.InvokeOutput
		err error
	}

	invoked := make(chan result, 1)

	go func() {
		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "real-fn"})
		invoked <- result{out, err}
	}()

	<-eng.entered
	fc.Advance(2 * time.Second)

	// The next code update retires "real-fn" while the invocation still runs.
	updated := make(chan error, 1)

	go func() {
		_, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v3")})
		updated <- err
	}()

	select {
	case err := <-updated:
		// Without waiting, the update has already removed the deployment.
		close(eng.release)
		t.Fatalf("code update returned (err %v) while an invocation of the deployment it retires was running", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(eng.release)

	res := <-invoked
	if res.err != nil {
		t.Fatalf("invocation running on the retired deployment failed: %v", res.err)
	}

	if got := string(res.out.Payload); got != "v1" {
		t.Errorf("invocation ran %q, want v1", got)
	}

	if err := <-updated; err != nil {
		t.Fatalf("UpdateFunction v3: %v", err)
	}

	for _, k := range eng.keys() {
		if k == "real-fn" {
			t.Errorf("retired deployment %q still deployed after the update: %v", k, eng.keys())
		}
	}
}
