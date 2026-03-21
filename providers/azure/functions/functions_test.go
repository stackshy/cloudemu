package functions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
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

func TestPublishVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6", Handler: "MyApp::Handler"})
	require.NoError(t, err)

	t.Run("publish first version", func(t *testing.T) {
		ver, err := m.PublishVersion(ctx, "fn1", "first release")
		require.NoError(t, err)
		assert.Equal(t, "1", ver.Version)
		assert.Equal(t, "fn1", ver.FunctionName)
		assert.Equal(t, "first release", ver.Description)
		assert.NotEmpty(t, ver.CodeSHA256)
		assert.NotEmpty(t, ver.CreatedAt)
	})

	t.Run("publish second version", func(t *testing.T) {
		ver, err := m.PublishVersion(ctx, "fn1", "second release")
		require.NoError(t, err)
		assert.Equal(t, "2", ver.Version)
	})

	t.Run("list versions includes $LATEST and published", func(t *testing.T) {
		versions, err := m.ListVersions(ctx, "fn1")
		require.NoError(t, err)
		assert.Len(t, versions, 3) // $LATEST + 2 published
		assert.Equal(t, "$LATEST", versions[0].Version)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.PublishVersion(ctx, "missing", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreateAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v1")
	require.NoError(t, err)

	tests := []struct {
		name    string
		cfg     driver.AliasConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "create alias to version 1",
			cfg: driver.AliasConfig{
				FunctionName: "fn1", Name: "prod", FunctionVersion: "1", Description: "production slot",
			},
		},
		{
			name: "create alias to $LATEST",
			cfg: driver.AliasConfig{
				FunctionName: "fn1", Name: "staging", FunctionVersion: "$LATEST",
			},
		},
		{
			name: "duplicate alias",
			cfg: driver.AliasConfig{
				FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
			},
			wantErr: true, errMsg: "already exists",
		},
		{
			name: "version not found",
			cfg: driver.AliasConfig{
				FunctionName: "fn1", Name: "bad", FunctionVersion: "999",
			},
			wantErr: true, errMsg: "not found",
		},
		{
			name: "function not found",
			cfg: driver.AliasConfig{
				FunctionName: "missing", Name: "alias", FunctionVersion: "1",
			},
			wantErr: true, errMsg: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.CreateAlias(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, result.Name)
				assert.Equal(t, tt.cfg.FunctionVersion, result.FunctionVersion)
				assert.NotEmpty(t, result.AliasARN)
			}
		})
	}
}

func TestUpdateAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v1")
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v2")
	require.NoError(t, err)

	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	t.Run("update version", func(t *testing.T) {
		result, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "prod", FunctionVersion: "2",
		})
		require.NoError(t, err)
		assert.Equal(t, "2", result.FunctionVersion)
	})

	t.Run("alias not found", func(t *testing.T) {
		_, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "missing", FunctionVersion: "1",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "missing", Name: "prod", FunctionVersion: "1",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v1")
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		fnName   string
		alias    string
		wantErr  bool
		errMsg   string
	}{
		{name: "success", fnName: "fn1", alias: "prod"},
		{name: "alias not found", fnName: "fn1", alias: "missing", wantErr: true, errMsg: "not found"},
		{name: "function not found", fnName: "missing", alias: "prod", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteAlias(ctx, tt.fnName, tt.alias)

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

func TestPublishLayerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("publish first version", func(t *testing.T) {
		lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name:               "my-extension",
			Description:        "shared utils",
			Content:            []byte("extension-code"),
			CompatibleRuntimes: []string{"dotnet6", "dotnet8"},
		})
		require.NoError(t, err)
		assert.Equal(t, "my-extension", lv.Name)
		assert.Equal(t, 1, lv.Version)
		assert.NotEmpty(t, lv.ContentSHA256)
		assert.Equal(t, int64(14), lv.ContentSize)
		assert.NotEmpty(t, lv.ARN)
	})

	t.Run("publish second version", func(t *testing.T) {
		lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name:    "my-extension",
			Content: []byte("updated-code"),
		})
		require.NoError(t, err)
		assert.Equal(t, 2, lv.Version)
	})

	t.Run("list versions", func(t *testing.T) {
		versions, err := m.ListLayerVersions(ctx, "my-extension")
		require.NoError(t, err)
		assert.Len(t, versions, 2)
	})

	t.Run("get specific version", func(t *testing.T) {
		lv, err := m.GetLayerVersion(ctx, "my-extension", 1)
		require.NoError(t, err)
		assert.Equal(t, 1, lv.Version)
		assert.Equal(t, "shared utils", lv.Description)
	})
}

func TestDeleteLayerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "ext", Content: []byte("code"),
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		layer   string
		version int
		wantErr bool
		errMsg  string
	}{
		{name: "success", layer: "ext", version: 1},
		{name: "version not found", layer: "ext", version: 99, wantErr: true, errMsg: "not found"},
		{name: "extension not found", layer: "missing", version: 1, wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteLayerVersion(ctx, tt.layer, tt.version)

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

func TestPutFunctionConcurrency(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)

	t.Run("set concurrency", func(t *testing.T) {
		err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName: "fn1", ReservedConcurrentExecutions: 10,
		})
		require.NoError(t, err)

		cfg, err := m.GetFunctionConcurrency(ctx, "fn1")
		require.NoError(t, err)
		assert.Equal(t, 10, cfg.ReservedConcurrentExecutions)
	})

	t.Run("update concurrency", func(t *testing.T) {
		err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName: "fn1", ReservedConcurrentExecutions: 50,
		})
		require.NoError(t, err)

		cfg, err := m.GetFunctionConcurrency(ctx, "fn1")
		require.NoError(t, err)
		assert.Equal(t, 50, cfg.ReservedConcurrentExecutions)
	})

	t.Run("delete concurrency", func(t *testing.T) {
		err := m.DeleteFunctionConcurrency(ctx, "fn1")
		require.NoError(t, err)

		_, err = m.GetFunctionConcurrency(ctx, "fn1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no concurrency")
	})

	t.Run("function not found", func(t *testing.T) {
		err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName: "missing", ReservedConcurrentExecutions: 10,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestFunctionsMetricsEmission(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))
	m := New(opts)

	mon := &funcMetricsCollector{}
	m.SetMonitoring(mon)

	ctx := context.Background()
	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)

	m.RegisterHandler("fn1", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	t.Run("successful invoke emits metrics", func(t *testing.T) {
		mon.reset()
		_, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "fn1", Payload: []byte("test")})
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.Web/sites", "FunctionExecutionCount"))
		assert.True(t, mon.hasMetric("Microsoft.Web/sites", "FunctionExecutionUnits"))
		assert.False(t, mon.hasMetric("Microsoft.Web/sites", "FunctionErrors"))
	})

	t.Run("failed invoke emits error metrics", func(t *testing.T) {
		m.RegisterHandler("fn1", func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, errors.New("boom")
		})
		mon.reset()
		_, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "fn1"})
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.Web/sites", "FunctionErrors"))
	})
}

type funcMetricsCollector struct {
	data []mondriver.MetricDatum
}

func (c *funcMetricsCollector) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	c.data = append(c.data, data...)
	return nil
}

func (c *funcMetricsCollector) GetMetricData(_ context.Context, _ mondriver.GetMetricInput) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (c *funcMetricsCollector) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (c *funcMetricsCollector) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (c *funcMetricsCollector) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (c *funcMetricsCollector) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *funcMetricsCollector) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *funcMetricsCollector) reset() {
	c.data = nil
}

func (c *funcMetricsCollector) hasMetric(namespace, metricName string) bool {
	for _, d := range c.data {
		if d.Namespace == namespace && d.MetricName == metricName {
			return true
		}
	}
	return false
}

func TestListVersions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("function not found", func(t *testing.T) {
		_, err := m.ListVersions(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("only LATEST when no published versions", func(t *testing.T) {
		_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn-ver", Runtime: "dotnet6", Handler: "h"})
		require.NoError(t, err)

		versions, err := m.ListVersions(ctx, "fn-ver")
		require.NoError(t, err)
		require.Len(t, versions, 1)
		assert.Equal(t, "$LATEST", versions[0].Version)
	})

	t.Run("multiple published versions", func(t *testing.T) {
		_, err := m.PublishVersion(ctx, "fn-ver", "v1")
		require.NoError(t, err)
		_, err = m.PublishVersion(ctx, "fn-ver", "v2")
		require.NoError(t, err)
		_, err = m.PublishVersion(ctx, "fn-ver", "v3")
		require.NoError(t, err)

		versions, err := m.ListVersions(ctx, "fn-ver")
		require.NoError(t, err)
		assert.Len(t, versions, 4) // $LATEST + 3 published
		assert.Equal(t, "$LATEST", versions[0].Version)
		assert.Equal(t, "1", versions[1].Version)
		assert.Equal(t, "2", versions[2].Version)
		assert.Equal(t, "3", versions[3].Version)
	})
}

func TestGetAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v1")
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		alias, err := m.GetAlias(ctx, "fn1", "prod")
		require.NoError(t, err)
		assert.Equal(t, "prod", alias.Name)
		assert.Equal(t, "1", alias.FunctionVersion)
		assert.Equal(t, "fn1", alias.FunctionName)
		assert.NotEmpty(t, alias.AliasARN)
	})

	t.Run("alias not found", func(t *testing.T) {
		_, err := m.GetAlias(ctx, "fn1", "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.GetAlias(ctx, "missing-fn", "prod")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListAliases(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v1")
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v2")
	require.NoError(t, err)

	t.Run("empty", func(t *testing.T) {
		aliases, err := m.ListAliases(ctx, "fn1")
		require.NoError(t, err)
		assert.Empty(t, aliases)
	})

	t.Run("multiple aliases", func(t *testing.T) {
		_, err := m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
		})
		require.NoError(t, err)

		_, err = m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "staging", FunctionVersion: "2",
		})
		require.NoError(t, err)

		aliases, err := m.ListAliases(ctx, "fn1")
		require.NoError(t, err)
		assert.Len(t, aliases, 2)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.ListAliases(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestUpdateAliasWithRoutingConfig(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn1", Runtime: "dotnet6"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v1")
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "fn1", "v2")
	require.NoError(t, err)

	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	t.Run("update with routing config", func(t *testing.T) {
		rc := &driver.AliasRoutingConfig{
			AdditionalVersion: "2",
			Weight:            0.1,
		}
		result, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "prod", FunctionVersion: "1",
			RoutingConfig: rc,
		})
		require.NoError(t, err)
		require.NotNil(t, result.RoutingConfig)
		assert.Equal(t, "2", result.RoutingConfig.AdditionalVersion)
		assert.InDelta(t, 0.1, result.RoutingConfig.Weight, 0.001)
	})

	t.Run("update description only", func(t *testing.T) {
		result, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "prod", Description: "updated desc",
		})
		require.NoError(t, err)
		assert.Equal(t, "updated desc", result.Description)
		// Routing config should be preserved from previous update
		require.NotNil(t, result.RoutingConfig)
	})

	t.Run("update version not found", func(t *testing.T) {
		_, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "fn1", Name: "prod", FunctionVersion: "999",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGetLayerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "ext1", Content: []byte("code"), Description: "test extension",
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		lv, err := m.GetLayerVersion(ctx, "ext1", 1)
		require.NoError(t, err)
		assert.Equal(t, "ext1", lv.Name)
		assert.Equal(t, 1, lv.Version)
		assert.Equal(t, "test extension", lv.Description)
		assert.NotEmpty(t, lv.ContentSHA256)
		assert.NotEmpty(t, lv.ARN)
	})

	t.Run("version not found", func(t *testing.T) {
		_, err := m.GetLayerVersion(ctx, "ext1", 99)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("extension not found", func(t *testing.T) {
		_, err := m.GetLayerVersion(ctx, "missing", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListLayers(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		m := newTestMock()
		layers, err := m.ListLayers(ctx)
		require.NoError(t, err)
		assert.Empty(t, layers)
	})

	t.Run("multiple layers with latest versions", func(t *testing.T) {
		m := newTestMock()

		_, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name: "ext-a", Content: []byte("a-v1"),
		})
		require.NoError(t, err)
		_, err = m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name: "ext-a", Content: []byte("a-v2"),
		})
		require.NoError(t, err)

		_, err = m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name: "ext-b", Content: []byte("b-v1"),
		})
		require.NoError(t, err)

		layers, err := m.ListLayers(ctx)
		require.NoError(t, err)
		assert.Len(t, layers, 2)

		// Verify we get latest version for each layer
		for _, layer := range layers {
			switch layer.Name {
			case "ext-a":
				assert.Equal(t, 2, layer.Version)
			case "ext-b":
				assert.Equal(t, 1, layer.Version)
			}
		}
	})
}

func TestGetFunctionConcurrencyNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("function not found", func(t *testing.T) {
		_, err := m.GetFunctionConcurrency(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("no concurrency set", func(t *testing.T) {
		_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn-nocc", Runtime: "dotnet6"})
		require.NoError(t, err)

		_, err = m.GetFunctionConcurrency(ctx, "fn-nocc")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no concurrency")
	})
}

func TestDeleteFunctionConcurrency(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("function not found", func(t *testing.T) {
		err := m.DeleteFunctionConcurrency(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("success removes concurrency config", func(t *testing.T) {
		_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn-del-cc", Runtime: "dotnet6"})
		require.NoError(t, err)

		err = m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName: "fn-del-cc", ReservedConcurrentExecutions: 25,
		})
		require.NoError(t, err)

		// Verify it exists
		cfg, err := m.GetFunctionConcurrency(ctx, "fn-del-cc")
		require.NoError(t, err)
		assert.Equal(t, 25, cfg.ReservedConcurrentExecutions)

		// Delete it
		err = m.DeleteFunctionConcurrency(ctx, "fn-del-cc")
		require.NoError(t, err)

		// Verify it's gone
		_, err = m.GetFunctionConcurrency(ctx, "fn-del-cc")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no concurrency")
	})

	t.Run("delete when no concurrency set is idempotent", func(t *testing.T) {
		_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "fn-del-cc2", Runtime: "dotnet6"})
		require.NoError(t, err)

		// Delete without ever setting - should still succeed
		err = m.DeleteFunctionConcurrency(ctx, "fn-del-cc2")
		require.NoError(t, err)
	})
}
