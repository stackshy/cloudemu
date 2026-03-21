package cloudfunctions

import (
	"context"
	"errors"
	"sync"
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

func TestPublishVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{
		Name: "f1", Runtime: "go121", Handler: "main.Handler",
	})
	require.NoError(t, err)

	t.Run("publish first version", func(t *testing.T) {
		ver, pubErr := m.PublishVersion(ctx, "f1", "initial release")
		require.NoError(t, pubErr)
		assert.Equal(t, "1", ver.Version)
		assert.Equal(t, "f1", ver.FunctionName)
		assert.NotEmpty(t, ver.CodeSHA256)
		assert.NotEmpty(t, ver.CreatedAt)
	})

	t.Run("publish second version", func(t *testing.T) {
		ver, pubErr := m.PublishVersion(ctx, "f1", "v2")
		require.NoError(t, pubErr)
		assert.Equal(t, "2", ver.Version)
	})

	t.Run("list versions includes $LATEST and published", func(t *testing.T) {
		versions, listErr := m.ListVersions(ctx, "f1")
		require.NoError(t, listErr)
		require.Len(t, versions, 3) // $LATEST + 2 published
		assert.Equal(t, "$LATEST", versions[0].Version)
	})

	t.Run("function not found", func(t *testing.T) {
		_, pubErr := m.PublishVersion(ctx, "missing", "")
		require.Error(t, pubErr)
		assert.Contains(t, pubErr.Error(), "not found")
	})
}

func TestCreateAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f1", "v1")
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.AliasConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg: driver.AliasConfig{
				FunctionName: "f1", Name: "prod",
				FunctionVersion: "1", Description: "production",
			},
		},
		{
			name: "duplicate alias",
			cfg: driver.AliasConfig{
				FunctionName: "f1", Name: "prod", FunctionVersion: "1",
			},
			wantErr:   true,
			errSubstr: "already exists",
		},
		{
			name: "function not found",
			cfg: driver.AliasConfig{
				FunctionName: "missing", Name: "dev", FunctionVersion: "1",
			},
			wantErr:   true,
			errSubstr: "not found",
		},
		{
			name: "version not found",
			cfg: driver.AliasConfig{
				FunctionName: "f1", Name: "staging", FunctionVersion: "99",
			},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, createErr := m.CreateAlias(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, createErr)
				assert.Contains(t, createErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, createErr)
				assert.Equal(t, "prod", alias.Name)
				assert.Equal(t, "1", alias.FunctionVersion)
				assert.NotEmpty(t, alias.AliasARN)
			}
		})
	}
}

func TestUpdateAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f1", "v1")
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f1", "v2")
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "f1", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		cfg       driver.AliasConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "update version",
			cfg: driver.AliasConfig{
				FunctionName: "f1", Name: "prod",
				FunctionVersion: "2", Description: "updated",
			},
		},
		{
			name: "alias not found",
			cfg: driver.AliasConfig{
				FunctionName: "f1", Name: "missing", FunctionVersion: "1",
			},
			wantErr:   true,
			errSubstr: "not found",
		},
		{
			name: "function not found",
			cfg: driver.AliasConfig{
				FunctionName: "missing", Name: "prod", FunctionVersion: "1",
			},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, updateErr := m.UpdateAlias(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, updateErr)
				assert.Contains(t, updateErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, updateErr)
				assert.Equal(t, "2", alias.FunctionVersion)
				assert.Equal(t, "updated", alias.Description)
			}
		})
	}
}

func TestDeleteAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f1", "v1")
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "f1", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		funcName  string
		aliasName string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", funcName: "f1", aliasName: "prod"},
		{name: "already deleted", funcName: "f1", aliasName: "prod", wantErr: true, errSubstr: "not found"},
		{name: "function not found", funcName: "missing", aliasName: "x", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delErr := m.DeleteAlias(ctx, tt.funcName, tt.aliasName)
			switch {
			case tt.wantErr:
				require.Error(t, delErr)
				assert.Contains(t, delErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, delErr)
			}
		})
	}
}

func TestPublishLayerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("publish first version", func(t *testing.T) {
		lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name:               "my-layer",
			Description:        "test layer",
			Content:            []byte("layer-content"),
			CompatibleRuntimes: []string{"go121"},
		})
		require.NoError(t, err)
		assert.Equal(t, "my-layer", lv.Name)
		assert.Equal(t, 1, lv.Version)
		assert.Equal(t, int64(13), lv.ContentSize)
		assert.NotEmpty(t, lv.ContentSHA256)
		assert.NotEmpty(t, lv.ARN)
	})

	t.Run("publish second version", func(t *testing.T) {
		lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name:    "my-layer",
			Content: []byte("updated-content"),
		})
		require.NoError(t, err)
		assert.Equal(t, 2, lv.Version)
	})

	t.Run("get specific version", func(t *testing.T) {
		lv, err := m.GetLayerVersion(ctx, "my-layer", 1)
		require.NoError(t, err)
		assert.Equal(t, 1, lv.Version)
		assert.Equal(t, "test layer", lv.Description)
	})

	t.Run("list layer versions", func(t *testing.T) {
		versions, err := m.ListLayerVersions(ctx, "my-layer")
		require.NoError(t, err)
		assert.Len(t, versions, 2)
	})
}

func TestDeleteLayerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "my-layer", Content: []byte("data"),
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		layerName string
		version   int
		wantErr   bool
		errSubstr string
	}{
		{name: "success", layerName: "my-layer", version: 1},
		{name: "version not found", layerName: "my-layer", version: 1, wantErr: true, errSubstr: "not found"},
		{name: "layer not found", layerName: "missing", version: 1, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delErr := m.DeleteLayerVersion(ctx, tt.layerName, tt.version)
			switch {
			case tt.wantErr:
				require.Error(t, delErr)
				assert.Contains(t, delErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, delErr)
			}
		})
	}
}

func TestPutFunctionConcurrency(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)

	t.Run("set concurrency", func(t *testing.T) {
		require.NoError(t, m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName:                 "f1",
			ReservedConcurrentExecutions: 100,
		}))

		cfg, getErr := m.GetFunctionConcurrency(ctx, "f1")
		require.NoError(t, getErr)
		assert.Equal(t, 100, cfg.ReservedConcurrentExecutions)
	})

	t.Run("delete concurrency", func(t *testing.T) {
		require.NoError(t, m.DeleteFunctionConcurrency(ctx, "f1"))

		_, getErr := m.GetFunctionConcurrency(ctx, "f1")
		require.Error(t, getErr)
		assert.Contains(t, getErr.Error(), "no concurrency config")
	})

	t.Run("function not found", func(t *testing.T) {
		putErr := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName: "missing", ReservedConcurrentExecutions: 50,
		})
		require.Error(t, putErr)
		assert.Contains(t, putErr.Error(), "not found")
	})
}

func TestCloudFunctionsMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	mon := &cfMonMock{data: make(map[string][]mondriver.MetricDatum)}
	m := New(opts)
	m.SetMonitoring(mon)

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f1", Runtime: "go121"})
	require.NoError(t, err)
	m.RegisterHandler("f1", func(_ context.Context, p []byte) ([]byte, error) {
		return p, nil
	})

	t.Run("successful invoke emits execution metrics", func(t *testing.T) {
		_, invokeErr := m.Invoke(ctx, driver.InvokeInput{FunctionName: "f1", Payload: []byte("hi")})
		require.NoError(t, invokeErr)

		assert.NotEmpty(t, mon.data["cloudfunctions.googleapis.com/function/execution_count"])
		assert.NotEmpty(t, mon.data["cloudfunctions.googleapis.com/function/execution_times"])
	})

	t.Run("failed invoke emits error metric", func(t *testing.T) {
		m.RegisterHandler("f1", func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, errors.New("fail")
		})

		_, invokeErr := m.Invoke(ctx, driver.InvokeInput{FunctionName: "f1"})
		require.NoError(t, invokeErr) // Invoke itself doesn't error, returns 500 status

		assert.NotEmpty(t, mon.data["cloudfunctions.googleapis.com/function/error_count"])
	})
}

type cfMonMock struct {
	data map[string][]mondriver.MetricDatum
}

func (m *cfMonMock) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	for _, d := range data {
		key := d.Namespace + "/" + d.MetricName
		m.data[key] = append(m.data[key], d)
	}

	return nil
}

func (m *cfMonMock) GetMetricData(
	_ context.Context, _ mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (m *cfMonMock) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (m *cfMonMock) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (m *cfMonMock) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (m *cfMonMock) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (m *cfMonMock) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func TestListVersionsMultiple(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{
		Name: "f-ver", Runtime: "go121", Handler: "main.Handler",
	})
	require.NoError(t, err)

	// Publish three versions
	for i := 0; i < 3; i++ {
		_, pubErr := m.PublishVersion(ctx, "f-ver", "")
		require.NoError(t, pubErr)
	}

	t.Run("lists $LATEST plus all published versions", func(t *testing.T) {
		versions, err := m.ListVersions(ctx, "f-ver")
		require.NoError(t, err)
		require.Len(t, versions, 4) // $LATEST + 3 published
		assert.Equal(t, "$LATEST", versions[0].Version)
		assert.Equal(t, "1", versions[1].Version)
		assert.Equal(t, "2", versions[2].Version)
		assert.Equal(t, "3", versions[3].Version)
	})

	t.Run("function not found", func(t *testing.T) {
		_, listErr := m.ListVersions(ctx, "missing")
		require.Error(t, listErr)
		assert.Contains(t, listErr.Error(), "not found")
	})
}

func TestGetAlias(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-alias", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f-alias", "v1")
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "f-alias", Name: "prod", FunctionVersion: "1", Description: "production",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		funcName  string
		aliasName string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", funcName: "f-alias", aliasName: "prod"},
		{name: "alias not found", funcName: "f-alias", aliasName: "missing", wantErr: true, errSubstr: "not found"},
		{name: "function not found", funcName: "missing", aliasName: "prod", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, err := m.GetAlias(ctx, tt.funcName, tt.aliasName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "prod", alias.Name)
				assert.Equal(t, "1", alias.FunctionVersion)
				assert.Equal(t, "production", alias.Description)
				assert.NotEmpty(t, alias.AliasARN)
			}
		})
	}
}

func TestListAliases(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-la", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f-la", "v1")
	require.NoError(t, err)

	t.Run("empty list", func(t *testing.T) {
		aliases, listErr := m.ListAliases(ctx, "f-la")
		require.NoError(t, listErr)
		assert.Empty(t, aliases)
	})

	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "f-la", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "f-la", Name: "staging", FunctionVersion: "$LATEST",
	})
	require.NoError(t, err)

	t.Run("two aliases", func(t *testing.T) {
		aliases, listErr := m.ListAliases(ctx, "f-la")
		require.NoError(t, listErr)
		assert.Len(t, aliases, 2)
	})

	t.Run("function not found", func(t *testing.T) {
		_, listErr := m.ListAliases(ctx, "missing")
		require.Error(t, listErr)
		assert.Contains(t, listErr.Error(), "not found")
	})
}

func TestUpdateAliasRoutingConfig(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-rc", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f-rc", "v1")
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f-rc", "v2")
	require.NoError(t, err)
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "f-rc", Name: "prod", FunctionVersion: "1",
	})
	require.NoError(t, err)

	t.Run("update with routing config", func(t *testing.T) {
		alias, updateErr := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "f-rc", Name: "prod",
			FunctionVersion: "1",
			RoutingConfig: &driver.AliasRoutingConfig{
				AdditionalVersion: "2",
				Weight:            0.1,
			},
		})
		require.NoError(t, updateErr)
		require.NotNil(t, alias.RoutingConfig)
		assert.Equal(t, "2", alias.RoutingConfig.AdditionalVersion)
		assert.InDelta(t, 0.1, alias.RoutingConfig.Weight, 0.001)
	})

	t.Run("update version to non-existent fails", func(t *testing.T) {
		_, updateErr := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "f-rc", Name: "prod", FunctionVersion: "99",
		})
		require.Error(t, updateErr)
		assert.Contains(t, updateErr.Error(), "not found")
	})
}

func TestGetLayerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "layer-get", Content: []byte("data"), Description: "test",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		layerName string
		version   int
		wantErr   bool
		errSubstr string
	}{
		{name: "success", layerName: "layer-get", version: 1},
		{name: "version not found", layerName: "layer-get", version: 99, wantErr: true, errSubstr: "not found"},
		{name: "layer not found", layerName: "missing-layer", version: 1, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lv, err := m.GetLayerVersion(ctx, tt.layerName, tt.version)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "layer-get", lv.Name)
				assert.Equal(t, 1, lv.Version)
				assert.Equal(t, "test", lv.Description)
			}
		})
	}
}

func TestListLayers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty list", func(t *testing.T) {
		layers, err := m.ListLayers(ctx)
		require.NoError(t, err)
		assert.Empty(t, layers)
	})

	// Publish versions for two different layers
	_, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "layer-a", Content: []byte("a1"),
	})
	require.NoError(t, err)
	_, err = m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "layer-a", Content: []byte("a2"),
	})
	require.NoError(t, err)
	_, err = m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "layer-b", Content: []byte("b1"),
	})
	require.NoError(t, err)

	t.Run("returns latest version of each layer", func(t *testing.T) {
		layers, listErr := m.ListLayers(ctx)
		require.NoError(t, listErr)
		assert.Len(t, layers, 2)

		// Find layer-a in results - should be version 2 (the latest)
		var foundA bool
		for _, l := range layers {
			if l.Name == "layer-a" {
				assert.Equal(t, 2, l.Version)
				foundA = true
			}
		}
		assert.True(t, foundA, "layer-a should be in results")
	})
}

func TestGetFunctionConcurrency(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-conc", Runtime: "go121"})
	require.NoError(t, err)

	t.Run("no concurrency set", func(t *testing.T) {
		_, getErr := m.GetFunctionConcurrency(ctx, "f-conc")
		require.Error(t, getErr)
		assert.Contains(t, getErr.Error(), "no concurrency config")
	})

	require.NoError(t, m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
		FunctionName:                 "f-conc",
		ReservedConcurrentExecutions: 50,
	}))

	t.Run("success", func(t *testing.T) {
		cfg, getErr := m.GetFunctionConcurrency(ctx, "f-conc")
		require.NoError(t, getErr)
		assert.Equal(t, 50, cfg.ReservedConcurrentExecutions)
		assert.Equal(t, "f-conc", cfg.FunctionName)
	})

	t.Run("function not found", func(t *testing.T) {
		_, getErr := m.GetFunctionConcurrency(ctx, "missing")
		require.Error(t, getErr)
		assert.Contains(t, getErr.Error(), "not found")
	})
}

func TestDeleteFunctionConcurrency(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-delconc", Runtime: "go121"})
	require.NoError(t, err)

	require.NoError(t, m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
		FunctionName:                 "f-delconc",
		ReservedConcurrentExecutions: 100,
	}))

	t.Run("delete concurrency", func(t *testing.T) {
		require.NoError(t, m.DeleteFunctionConcurrency(ctx, "f-delconc"))

		_, getErr := m.GetFunctionConcurrency(ctx, "f-delconc")
		require.Error(t, getErr)
		assert.Contains(t, getErr.Error(), "no concurrency config")
	})

	t.Run("function not found", func(t *testing.T) {
		delErr := m.DeleteFunctionConcurrency(ctx, "missing")
		require.Error(t, delErr)
		assert.Contains(t, delErr.Error(), "not found")
	})
}

func TestPublishVersionConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-conc", Runtime: "go121"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			_, _ = m.PublishVersion(ctx, "f-conc", "concurrent")
		}()
	}

	wg.Wait()

	versions, err := m.ListVersions(ctx, "f-conc")
	require.NoError(t, err)
	require.Len(t, versions, 11) // $LATEST + 10 published

	seen := make(map[string]bool)
	for _, v := range versions {
		assert.False(t, seen[v.Version], "duplicate version: %s", v.Version)
		seen[v.Version] = true
	}
}

func TestAliasRoutingConfigDeepCopy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "f-dc", Runtime: "go121"})
	require.NoError(t, err)
	_, err = m.PublishVersion(ctx, "f-dc", "v1")
	require.NoError(t, err)

	rc := &driver.AliasRoutingConfig{
		AdditionalVersion: "1",
		Weight:            0.5,
	}

	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "f-dc",
		Name:            "deep-copy",
		FunctionVersion: "$LATEST",
		RoutingConfig:   rc,
	})
	require.NoError(t, err)

	// Mutate the original config
	rc.Weight = 0.9

	alias, err := m.GetAlias(ctx, "f-dc", "deep-copy")
	require.NoError(t, err)
	require.NotNil(t, alias.RoutingConfig)
	assert.InDelta(t, 0.5, alias.RoutingConfig.Weight, 0.001)
}
