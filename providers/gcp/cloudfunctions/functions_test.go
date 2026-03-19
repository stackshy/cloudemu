package cloudfunctions

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
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts)
}

func TestCreateFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.FunctionConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg: driver.FunctionConfig{
				Name: "my-func", Runtime: "go121", Handler: "main.Handler",
				Memory: 256, Timeout: 30,
				Environment: map[string]string{"KEY": "VAL"},
				Tags:        map[string]string{"env": "test"},
			},
		},
		{name: "duplicate", cfg: driver.FunctionConfig{Name: "my-func"}, wantErr: true, errSubstr: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateFunction(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "my-func", info.Name)
				assert.Equal(t, "ACTIVE", info.State)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, "go121", info.Runtime)
				assert.Equal(t, 256, info.Memory)
			}
		})
	}
}

func TestDeleteFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		funcName  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", funcName: "f1"},
		{name: "not found", funcName: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteFunction(ctx, tt.funcName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121", Handler: "main.Handler"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		funcName  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", funcName: "f1"},
		{name: "not found", funcName: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.GetFunction(ctx, tt.funcName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "f1", info.Name)
				assert.Equal(t, "main.Handler", info.Handler)
			}
		})
	}
}

func TestListFunctions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	funcs, err := m.ListFunctions(ctx)
	require.NoError(t, err)
	assert.Empty(t, funcs)

	_, err = m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.CreateFunction(ctx, driver.FunctionConfig{Name: "f2", Runtime: "python312"})
	require.NoError(t, err)

	funcs, err = m.ListFunctions(ctx)
	require.NoError(t, err)
	assert.Len(t, funcs, 2)
}

func TestInvokeFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "echo", Runtime: "go121"})
	require.NoError(t, err)

	tests := []struct {
		name       string
		funcName   string
		handler    driver.HandlerFunc
		payload    []byte
		wantStatus int
		wantErr    bool
		errSubstr  string
	}{
		{name: "no handler", funcName: "echo", wantStatus: 500},
		{name: "with handler", funcName: "echo", handler: func(_ context.Context, p []byte) ([]byte, error) {
			return append([]byte("echo:"), p...), nil
		}, payload: []byte("hi"), wantStatus: 200},
		{name: "handler error", funcName: "echo", handler: func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, errors.New("fail")
		}, wantStatus: 500},
		{name: "function not found", funcName: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.handler != nil {
				m.RegisterHandler(tt.funcName, tt.handler)
			}
			out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: tt.funcName, Payload: tt.payload})
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantStatus, out.StatusCode)
			}
		})
	}
}

func TestUpdateFunction(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121", Memory: 128, Timeout: 30})
	require.NoError(t, err)

	tests := []struct {
		name      string
		funcName  string
		cfg       driver.FunctionConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "update memory and runtime", funcName: "f1", cfg: driver.FunctionConfig{Runtime: "go122", Memory: 512}},
		{name: "not found", funcName: "missing", cfg: driver.FunctionConfig{Memory: 256}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.UpdateFunction(ctx, tt.funcName, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "go122", info.Runtime)
				assert.Equal(t, 512, info.Memory)
				assert.Equal(t, 30, info.Timeout) // unchanged
			}
		})
	}
}
