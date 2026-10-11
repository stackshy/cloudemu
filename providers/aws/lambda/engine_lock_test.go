package lambda

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// unblockedRead is how long a read on another function may take while an
// engine call is in flight. The engine call itself blocks until released.
const unblockedRead = 100 * time.Millisecond

// gatedEngine is a FunctionEngine whose Deploy and Remove block until the test
// releases them, standing in for a slow real engine (a container build).
type gatedEngine struct {
	started chan string
	release chan struct{}
}

func newGatedEngine() *gatedEngine {
	return &gatedEngine{started: make(chan string, 4), release: make(chan struct{})}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (g *gatedEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	g.started <- fn.Name
	<-g.release

	return nil
}

func (*gatedEngine) Invoke(context.Context, string, []byte) (config.FunctionResult, error) {
	return config.FunctionResult{}, nil
}

func (g *gatedEngine) Remove(_ context.Context, name string) error {
	g.started <- name
	<-g.release

	return nil
}

// otherFunction creates a code-less function (no engine call) with a policy
// and a function URL, and returns the URL host.
func otherFunction(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "other", Runtime: "python3.12", Handler: "h"}); err != nil {
		t.Fatalf("CreateFunction(other): %v", err)
	}

	if err := m.AddPermission(ctx, "other", "", driver.PermissionStatement{
		StatementID: "s", Action: "lambda:InvokeFunction", Principal: "*",
	}); err != nil {
		t.Fatalf("AddPermission(other): %v", err)
	}

	cfg, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: "other", AuthType: "NONE"})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig(other): %v", err)
	}

	u, err := url.Parse(cfg.FunctionURL)
	if err != nil {
		t.Fatalf("parse %s: %v", cfg.FunctionURL, err)
	}

	return u.Host
}

// assertReadsNotBlocked checks the reads the auth gate and URL dispatch make
// on every request return promptly on an unrelated function.
func assertReadsNotBlocked(t *testing.T, m *Mock, host string) {
	t.Helper()

	ctx := context.Background()
	begin := time.Now()

	if _, stmts, err := m.PolicyStatements(ctx, "other", ""); err != nil || len(stmts) != 1 {
		t.Fatalf("PolicyStatements(other) = %v, %v", stmts, err)
	}

	if _, err := m.ResolveFunctionURL(ctx, host); err != nil {
		t.Fatalf("ResolveFunctionURL: %v", err)
	}

	if _, err := m.GetPolicy(ctx, "other", ""); err != nil {
		t.Fatalf("GetPolicy(other): %v", err)
	}

	if took := time.Since(begin); took > unblockedRead {
		t.Fatalf("reads on another function took %v while an engine call was in flight", took)
	}
}

func waitStarted(t *testing.T, g *gatedEngine, want string) {
	t.Helper()

	select {
	case got := <-g.started:
		if got != want {
			t.Fatalf("engine call for %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("engine call for %q never started", want)
	}
}

func TestEngineDeployDoesNotHoldLock(t *testing.T) {
	eng := newGatedEngine()
	m := newEngineMock(eng)
	host := otherFunction(t, m)
	ctx := context.Background()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateFunction(ctx, engineFuncConfig())
		created <- err
	}()

	waitStarted(t, eng, "real-fn")
	assertReadsNotBlocked(t, m, host)

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); !errors.IsAlreadyExists(err) {
		t.Fatalf("CreateFunction of a name being created err = %v, want AlreadyExists", err)
	}

	eng.release <- struct{}{}

	if err := <-created; err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// Code update: the deploy runs unlocked, and other changes to the same
	// function are refused until it finishes.
	updated := make(chan error, 1)

	go func() {
		_, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")})
		updated <- err
	}()

	waitStarted(t, eng, "real-fn")
	assertReadsNotBlocked(t, m, host)

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Timeout: 9}); !errors.IsAlreadyExists(err) {
		t.Fatalf("UpdateFunction during a code deploy err = %v, want AlreadyExists (ResourceConflict)", err)
	}

	if err := m.DeleteFunction(ctx, "real-fn"); !errors.IsAlreadyExists(err) {
		t.Fatalf("DeleteFunction during a code deploy err = %v, want AlreadyExists (ResourceConflict)", err)
	}

	if err := m.AddPermission(ctx, "real-fn", "", driver.PermissionStatement{
		StatementID: "during", Action: "lambda:InvokeFunction", Principal: "*",
	}); err != nil {
		t.Fatalf("AddPermission during a code deploy: %v", err)
	}

	eng.release <- struct{}{}

	if err := <-updated; err != nil {
		t.Fatalf("UpdateFunction(code): %v", err)
	}

	// The update was applied to the entry as it was after the deploy, so the
	// grant added meanwhile survived.
	if _, stmts, _ := m.PolicyStatements(ctx, "real-fn", ""); len(stmts) != 1 {
		t.Fatalf("policy after the code update = %v, want the grant added during the deploy", stmts)
	}

	// Delete: the function is gone at once; the name stays reserved until the
	// engine Remove returns.
	deleted := make(chan error, 1)

	go func() { deleted <- m.DeleteFunction(ctx, "real-fn") }()

	waitStarted(t, eng, "real-fn")
	assertReadsNotBlocked(t, m, host)

	if _, err := m.GetFunction(ctx, "real-fn"); !errors.IsNotFound(err) {
		t.Fatalf("GetFunction during the engine remove err = %v, want NotFound", err)
	}

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); !errors.IsAlreadyExists(err) {
		t.Fatalf("CreateFunction during the engine remove err = %v, want AlreadyExists", err)
	}

	eng.release <- struct{}{}

	if err := <-deleted; err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}
}

func TestConcurrentCreateFunctionSameName(t *testing.T) {
	const callers = 8

	m := newTestMock()
	start := make(chan struct{})

	var (
		wg sync.WaitGroup
		ok atomic.Int32
	)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			if _, err := m.CreateFunction(context.Background(), defaultFuncConfig()); err == nil {
				ok.Add(1)
			} else if !errors.IsAlreadyExists(err) {
				t.Errorf("CreateFunction err = %v, want nil or AlreadyExists", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := ok.Load(); got != 1 {
		t.Fatalf("%d CreateFunction calls succeeded, want exactly 1", got)
	}
}
