package functions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))

	return New(opts)
}

func TestCreateFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.FunctionConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg: driver.FunctionConfig{
				Name: "myFunc", Runtime: "dotnet6", Handler: "MyApp::Handler",
				Memory: 256, Timeout: 30, Environment: map[string]string{"ENV": "test"},
				Tags: map[string]string{"team": "backend"},
			},
		},
		{
			name:    "duplicate",
			cfg:     driver.FunctionConfig{Name: "myFunc", Runtime: "dotnet6"},
			wantErr: true, errMsg: "already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateFunction(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, info.Name)
				assert.Equal(t, "dotnet6", info.Runtime)
				assert.Equal(t, "Active", info.State)
				assert.NotEmpty(t, info.ARN)
				assert.NotEmpty(t, info.LastModified)
			}
		})
	}
}

func TestDeleteFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "node18"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		fnName  string
		wantErr bool
		errMsg  string
	}{
		{name: "success", fnName: "fn1"},
		{name: "not found", fnName: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteFunction(ctx, tt.fnName)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "python3.9", Handler: "main.handler"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := m.GetFunction(ctx, "fn1")
		require.NoError(t, err)
		assert.Equal(t, "fn1", info.Name)
		assert.Equal(t, "python3.9", info.Runtime)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetFunction(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListFunctions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty", func(t *testing.T) {
		fns, err := m.ListFunctions(ctx)
		require.NoError(t, err)
		assert.Empty(t, fns)
	})

	t.Run("with functions", func(t *testing.T) {
		_, _ = m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "go1.x"})
		_, _ = m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn2", Runtime: "go1.x"})

		fns, err := m.ListFunctions(ctx)
		require.NoError(t, err)
		assert.Len(t, fns, 2)
	})
}

func TestUpdateFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{
		Name: "fn1", Runtime: "node18", Handler: "index.handler", Memory: 128, Timeout: 10,
	})
	require.NoError(t, err)

	t.Run("update fields", func(t *testing.T) {
		info, err := m.UpdateFunction(ctx, "fn1", driver.FunctionConfig{
			Memory: 512, Timeout: 60, Environment: map[string]string{"KEY": "val"},
		})
		require.NoError(t, err)
		assert.Equal(t, 512, info.Memory)
		assert.Equal(t, 60, info.Timeout)
		assert.Equal(t, "val", info.Environment["KEY"])
		// Unchanged fields remain
		assert.Equal(t, "node18", info.Runtime)
		assert.Equal(t, "index.handler", info.Handler)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.UpdateFunction(ctx, "missing", driver.FunctionConfig{Memory: 256})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestInvokeFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "go1.x"})
	require.NoError(t, err)

	t.Run("no handler registered", func(t *testing.T) {
		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "fn1", Payload: []byte("test")})
		require.NoError(t, err)
		assert.Equal(t, 500, out.StatusCode)
		assert.Equal(t, "no handler registered", out.Error)
	})

	t.Run("with handler success", func(t *testing.T) {
		m.RegisterHandler("fn1", func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("echo: " + string(payload)), nil
		})

		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "fn1", Payload: []byte("hello")})
		require.NoError(t, err)
		assert.Equal(t, 200, out.StatusCode)
		assert.Equal(t, "echo: hello", string(out.Payload))
	})

	t.Run("with handler error", func(t *testing.T) {
		m.RegisterHandler("fn1", func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, errors.New("handler failed")
		})

		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "fn1", Payload: nil})
		require.NoError(t, err)
		assert.Equal(t, 500, out.StatusCode)
		assert.Equal(t, "handler failed", out.Error)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "missing"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRegisterHandlerBeforeCreate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// Register handler before creating function
	m.RegisterHandler("fn1", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("pre-registered"), nil
	})

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "go1.x"})
	require.NoError(t, err)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "fn1"})
	require.NoError(t, err)
	assert.Equal(t, 200, out.StatusCode)
	assert.Equal(t, "pre-registered", string(out.Payload))
}
