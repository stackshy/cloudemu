package lambda

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/serverless/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func defaultFuncConfig() driver.FunctionConfig {
	return driver.FunctionConfig{
		Name:    "my-func",
		Runtime: "go1.x",
		Handler: "main",
		Memory:  128,
		Timeout: 30,
		Tags:    map[string]string{"env": "test"},
	}
}

func TestCreateFunction(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.FunctionConfig
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "success", cfg: defaultFuncConfig()},
		{
			name: "already exists",
			cfg:  defaultFuncConfig(),
			setup: func(m *Mock) {
				_, _ = m.CreateFunction(context.Background(), defaultFuncConfig())
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			info, err := m.CreateFunction(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "my-func", info.Name)
			assertEqual(t, "go1.x", info.Runtime)
			assertEqual(t, "main", info.Handler)
			assertEqual(t, 128, info.Memory)
			assertEqual(t, 30, info.Timeout)
			assertEqual(t, "Active", info.State)
			assertNotEmpty(t, info.ARN)
		})
	}
}

func TestDeleteFunction(t *testing.T) {
	tests := []struct {
		name      string
		funcName  string
		setup     func(m *Mock)
		expectErr bool
	}{
		{
			name:     "success",
			funcName: "my-func",
			setup: func(m *Mock) {
				_, _ = m.CreateFunction(context.Background(), defaultFuncConfig())
			},
		},
		{name: "not found", funcName: "nope", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.DeleteFunction(context.Background(), tc.funcName)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestGetFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("found", func(t *testing.T) {
		info, err := m.GetFunction(ctx, "my-func")
		requireNoError(t, err)
		assertEqual(t, "my-func", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetFunction(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListFunctions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	fns, err := m.ListFunctions(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(fns))

	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	cfg2 := defaultFuncConfig()
	cfg2.Name = "other-func"
	_, _ = m.CreateFunction(ctx, cfg2)

	fns, err = m.ListFunctions(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(fns))
}

func TestUpdateFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("success", func(t *testing.T) {
		info, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{
			Runtime: "python3.9",
			Memory:  256,
		})
		requireNoError(t, err)
		assertEqual(t, "python3.9", info.Runtime)
		assertEqual(t, 256, info.Memory)
		// Handler should remain unchanged since empty
		assertEqual(t, "main", info.Handler)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.UpdateFunction(ctx, "nope", driver.FunctionConfig{Runtime: "x"})
		assertError(t, err, true)
	})
}

func TestInvokeFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("no handler returns error", func(t *testing.T) {
		out, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func",
			Payload:      []byte("test"),
		})
		requireNoError(t, err)
		assertEqual(t, 500, out.StatusCode)
		assertEqual(t, "no handler registered", out.Error)
	})

	t.Run("with handler success", func(t *testing.T) {
		m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("echo: " + string(payload)), nil
		})

		out, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func",
			Payload:      []byte("hello"),
		})
		requireNoError(t, err)
		assertEqual(t, 200, out.StatusCode)
		assertEqual(t, "echo: hello", string(out.Payload))
	})

	t.Run("handler returns error", func(t *testing.T) {
		m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, fmt.Errorf("handler failure")
		})

		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func"})
		requireNoError(t, err)
		assertEqual(t, 500, out.StatusCode)
		assertEqual(t, "handler failure", out.Error)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "nope"})
		assertError(t, err, true)
	})
}

func TestRegisterHandlerBeforeCreate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Register handler before function exists
	m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("pre-registered"), nil
	})

	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte("x")})
	requireNoError(t, err)
	assertEqual(t, 200, out.StatusCode)
	assertEqual(t, "pre-registered", string(out.Payload))
}

// --- test helpers ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}
