package lambda

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// createGated runs CreateFunction(real-fn) on a gated engine, releasing its
// deploy, and returns the result.
func createGated(t *testing.T, m *Mock, eng *gatedEngine) error {
	t.Helper()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateFunction(context.Background(), engineFuncConfig())
		created <- err
	}()

	waitStarted(t, eng, "real-fn")
	eng.release <- struct{}{}

	return <-created
}

func TestDeleteWinsOverInFlightCodeUpdate(t *testing.T) {
	eng := newGatedEngine()
	m := newEngineMock(eng)
	ctx := context.Background()

	if err := createGated(t, m, eng); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	updated := make(chan error, 1)

	go func() {
		_, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")})
		updated <- err
	}()

	waitStarted(t, eng, "real-fn")

	if err := m.DeleteFunction(ctx, "real-fn"); err != nil {
		t.Fatalf("DeleteFunction during a code deploy: %v", err)
	}

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); !errors.IsAlreadyExists(err) {
		t.Fatalf("CreateFunction while the deploy still runs err = %v, want AlreadyExists", err)
	}

	eng.release <- struct{}{}

	// The update's finalize finds the function gone and removes the deployment.
	waitStarted(t, eng, "real-fn")
	eng.release <- struct{}{}

	if err := <-updated; !errors.IsNotFound(err) {
		t.Fatalf("UpdateFunction after a concurrent delete err = %v, want NotFound", err)
	}

	if _, err := m.GetFunction(ctx, "real-fn"); !errors.IsNotFound(err) {
		t.Fatalf("GetFunction after delete err = %v, want NotFound", err)
	}

	if err := createGated(t, m, eng); err != nil {
		t.Fatalf("CreateFunction after the update finished: %v", err)
	}
}

// panicEngine panics in Deploy and Remove while armed.
type panicEngine struct{ armed atomic.Bool }

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (p *panicEngine) Deploy(context.Context, config.FunctionDeployment) error {
	if p.armed.Load() {
		panic("engine deploy failed")
	}

	return nil
}

func (*panicEngine) Invoke(context.Context, string, []byte) (config.FunctionResult, error) {
	return config.FunctionResult{}, nil
}

func (p *panicEngine) Remove(context.Context, string) error {
	if p.armed.Load() {
		panic("engine remove failed")
	}

	return nil
}

// panicking runs fn with the engine armed and reports whether fn panicked.
func panicking(eng *panicEngine, fn func()) (panicked bool) {
	eng.armed.Store(true)

	defer func() {
		eng.armed.Store(false)

		panicked = recover() != nil
	}()

	fn()

	return false
}

func TestEngineReservationReleasedOnPanic(t *testing.T) {
	eng := &panicEngine{}
	m := newEngineMock(eng)
	ctx := context.Background()

	if !panicking(eng, func() { _, _ = m.CreateFunction(ctx, engineFuncConfig()) }) {
		t.Fatal("CreateFunction did not panic")
	}

	if _, err := m.GetFunction(ctx, "real-fn"); !errors.IsNotFound(err) {
		t.Fatalf("GetFunction after a panicking create err = %v, want NotFound", err)
	}

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction after a panicking create: %v", err)
	}

	if !panicking(eng, func() { _, _ = m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")}) }) {
		t.Fatal("UpdateFunction did not panic")
	}

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v3")}); err != nil {
		t.Fatalf("UpdateFunction after a panicking update: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "real-fn", ""); err != nil {
		t.Fatalf("PublishVersion after a panicking update: %v", err)
	}

	if !panicking(eng, func() { _ = m.DeleteFunction(ctx, "real-fn") }) {
		t.Fatal("DeleteFunction did not panic")
	}

	if _, err := m.CreateFunction(ctx, engineFuncConfig()); err != nil {
		t.Fatalf("CreateFunction after a panicking delete: %v", err)
	}

	if err := m.DeleteFunction(ctx, "real-fn"); err != nil {
		t.Fatalf("DeleteFunction after a panicking delete: %v", err)
	}
}

func TestRestoreDuringCreateKeepsRestoredEntry(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()
	if _, err := src.CreateFunction(ctx, driver.FunctionConfig{
		Name: "real-fn", Runtime: "python3.12", Handler: "h", Description: "restored",
	}); err != nil {
		t.Fatalf("CreateFunction(src): %v", err)
	}

	snap, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	eng := newGatedEngine()
	m := newEngineMock(eng)
	created := make(chan error, 1)

	go func() {
		_, err := m.CreateFunction(ctx, engineFuncConfig())
		created <- err
	}()

	waitStarted(t, eng, "real-fn")

	if err := m.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	eng.release <- struct{}{}

	// The create's finalize keeps the restored entry and removes its deployment.
	waitStarted(t, eng, "real-fn")
	eng.release <- struct{}{}

	if err := <-created; !errors.IsAlreadyExists(err) {
		t.Fatalf("CreateFunction over a restored entry err = %v, want AlreadyExists", err)
	}

	info, err := m.GetFunction(ctx, "real-fn")
	if err != nil || info.Description != "restored" {
		t.Fatalf("GetFunction = %+v, %v; want the restored entry", info, err)
	}

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Timeout: 9}); err != nil {
		t.Fatalf("UpdateFunction after restore: %v", err)
	}
}
