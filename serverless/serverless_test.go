package serverless

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.Serverless for testing the portable wrapper.
type mockDriver struct {
	functions   map[string]*driver.FunctionInfo
	versions    map[string][]driver.FunctionVersion
	aliases     map[string]map[string]*driver.Alias
	layers      map[string][]driver.LayerVersion
	concurrency map[string]*driver.ConcurrencyConfig
	handlers    map[string]driver.HandlerFunc
	mappings    map[string]*driver.EventSourceMappingInfo
	versionSeq  int
	layerSeq    int
	mappingSeq  int
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		functions:   make(map[string]*driver.FunctionInfo),
		versions:    make(map[string][]driver.FunctionVersion),
		aliases:     make(map[string]map[string]*driver.Alias),
		layers:      make(map[string][]driver.LayerVersion),
		concurrency: make(map[string]*driver.ConcurrencyConfig),
		handlers:    make(map[string]driver.HandlerFunc),
		mappings:    make(map[string]*driver.EventSourceMappingInfo),
	}
}

func (m *mockDriver) CreateFunction(_ context.Context, config driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	if _, ok := m.functions[config.Name]; ok {
		return nil, fmt.Errorf("already exists")
	}

	info := &driver.FunctionInfo{
		Name:    config.Name,
		ARN:     "arn:aws:lambda:us-east-1:123456789012:function:" + config.Name,
		Runtime: config.Runtime,
		Handler: config.Handler,
		Memory:  config.Memory,
		Timeout: config.Timeout,
		State:   "Active",
	}
	m.functions[config.Name] = info

	return info, nil
}

func (m *mockDriver) DeleteFunction(_ context.Context, name string) error {
	if _, ok := m.functions[name]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.functions, name)

	return nil
}

func (m *mockDriver) GetFunction(_ context.Context, name string) (*driver.FunctionInfo, error) {
	info, ok := m.functions[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListFunctions(_ context.Context) ([]driver.FunctionInfo, error) {
	result := make([]driver.FunctionInfo, 0, len(m.functions))
	for _, info := range m.functions {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) UpdateFunction(_ context.Context, name string, config driver.FunctionConfig) (*driver.FunctionInfo, error) {
	info, ok := m.functions[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	if config.Runtime != "" {
		info.Runtime = config.Runtime
	}

	return info, nil
}

func (m *mockDriver) Invoke(_ context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	if _, ok := m.functions[input.FunctionName]; !ok {
		return nil, fmt.Errorf("not found")
	}

	return &driver.InvokeOutput{StatusCode: 200, Payload: []byte("ok")}, nil
}

func (m *mockDriver) RegisterHandler(name string, handler driver.HandlerFunc) {
	m.handlers[name] = handler
}

func (m *mockDriver) PublishVersion(_ context.Context, functionName, description string) (*driver.FunctionVersion, error) {
	if _, ok := m.functions[functionName]; !ok {
		return nil, fmt.Errorf("not found")
	}

	m.versionSeq++
	v := driver.FunctionVersion{
		FunctionName: functionName,
		Version:      fmt.Sprintf("%d", m.versionSeq),
		Description:  description,
	}
	m.versions[functionName] = append(m.versions[functionName], v)

	return &v, nil
}

func (m *mockDriver) ListVersions(_ context.Context, functionName string) ([]driver.FunctionVersion, error) {
	if _, ok := m.functions[functionName]; !ok {
		return nil, fmt.Errorf("not found")
	}

	return m.versions[functionName], nil
}

func (m *mockDriver) CreateAlias(_ context.Context, config driver.AliasConfig) (*driver.Alias, error) {
	if _, ok := m.functions[config.FunctionName]; !ok {
		return nil, fmt.Errorf("function not found")
	}

	if m.aliases[config.FunctionName] == nil {
		m.aliases[config.FunctionName] = make(map[string]*driver.Alias)
	}

	if _, ok := m.aliases[config.FunctionName][config.Name]; ok {
		return nil, fmt.Errorf("alias already exists")
	}

	a := &driver.Alias{
		FunctionName:    config.FunctionName,
		Name:            config.Name,
		FunctionVersion: config.FunctionVersion,
		Description:     config.Description,
	}
	m.aliases[config.FunctionName][config.Name] = a

	return a, nil
}

func (m *mockDriver) UpdateAlias(_ context.Context, config driver.AliasConfig) (*driver.Alias, error) {
	aliases, ok := m.aliases[config.FunctionName]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	a, ok := aliases[config.Name]
	if !ok {
		return nil, fmt.Errorf("alias not found")
	}

	a.FunctionVersion = config.FunctionVersion
	a.Description = config.Description

	return a, nil
}

func (m *mockDriver) DeleteAlias(_ context.Context, functionName, aliasName string) error {
	aliases, ok := m.aliases[functionName]
	if !ok {
		return fmt.Errorf("not found")
	}

	if _, ok := aliases[aliasName]; !ok {
		return fmt.Errorf("alias not found")
	}

	delete(aliases, aliasName)

	return nil
}

func (m *mockDriver) GetAlias(_ context.Context, functionName, aliasName string) (*driver.Alias, error) {
	aliases, ok := m.aliases[functionName]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	a, ok := aliases[aliasName]
	if !ok {
		return nil, fmt.Errorf("alias not found")
	}

	return a, nil
}

func (m *mockDriver) ListAliases(_ context.Context, functionName string) ([]driver.Alias, error) {
	if _, ok := m.functions[functionName]; !ok {
		return nil, fmt.Errorf("not found")
	}

	aliases := m.aliases[functionName]
	result := make([]driver.Alias, 0, len(aliases))

	for _, a := range aliases {
		result = append(result, *a)
	}

	return result, nil
}

func (m *mockDriver) PublishLayerVersion(_ context.Context, config driver.LayerConfig) (*driver.LayerVersion, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	m.layerSeq++
	lv := driver.LayerVersion{
		Name:        config.Name,
		Version:     m.layerSeq,
		Description: config.Description,
	}
	m.layers[config.Name] = append(m.layers[config.Name], lv)

	return &lv, nil
}

func (m *mockDriver) GetLayerVersion(_ context.Context, name string, version int) (*driver.LayerVersion, error) {
	versions, ok := m.layers[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	for idx := range versions {
		if versions[idx].Version == version {
			return &versions[idx], nil
		}
	}

	return nil, fmt.Errorf("version not found")
}

func (m *mockDriver) ListLayerVersions(_ context.Context, name string) ([]driver.LayerVersion, error) {
	versions, ok := m.layers[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return versions, nil
}

func (m *mockDriver) DeleteLayerVersion(_ context.Context, name string, version int) error {
	versions, ok := m.layers[name]
	if !ok {
		return fmt.Errorf("not found")
	}

	for idx := range versions {
		if versions[idx].Version == version {
			m.layers[name] = append(versions[:idx], versions[idx+1:]...)
			return nil
		}
	}

	return fmt.Errorf("version not found")
}

func (m *mockDriver) ListLayers(_ context.Context) ([]driver.LayerVersion, error) {
	var result []driver.LayerVersion

	for _, versions := range m.layers {
		if len(versions) > 0 {
			result = append(result, versions[len(versions)-1])
		}
	}

	return result, nil
}

func (m *mockDriver) PutFunctionConcurrency(_ context.Context, config driver.ConcurrencyConfig) error {
	if _, ok := m.functions[config.FunctionName]; !ok {
		return fmt.Errorf("not found")
	}

	m.concurrency[config.FunctionName] = &config

	return nil
}

func (m *mockDriver) GetFunctionConcurrency(_ context.Context, functionName string) (*driver.ConcurrencyConfig, error) {
	c, ok := m.concurrency[functionName]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return c, nil
}

func (m *mockDriver) DeleteFunctionConcurrency(_ context.Context, functionName string) error {
	if _, ok := m.concurrency[functionName]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.concurrency, functionName)

	return nil
}

func (m *mockDriver) CreateEventSourceMapping(
	_ context.Context, config driver.EventSourceMappingConfig,
) (*driver.EventSourceMappingInfo, error) {
	if config.FunctionName == "" {
		return nil, fmt.Errorf("function name required")
	}

	if config.EventSourceArn == "" {
		return nil, fmt.Errorf("event source ARN required")
	}

	m.mappingSeq++
	uuid := fmt.Sprintf("esm-%d", m.mappingSeq)

	state := "Disabled"
	if config.Enabled {
		state = "Enabled"
	}

	info := &driver.EventSourceMappingInfo{
		UUID:             uuid,
		EventSourceArn:   config.EventSourceArn,
		FunctionName:     config.FunctionName,
		BatchSize:        config.BatchSize,
		Enabled:          config.Enabled,
		StartingPosition: config.StartingPosition,
		State:            state,
	}
	m.mappings[uuid] = info

	return info, nil
}

func (m *mockDriver) DeleteEventSourceMapping(_ context.Context, uuid string) error {
	if _, ok := m.mappings[uuid]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.mappings, uuid)

	return nil
}

func (m *mockDriver) GetEventSourceMapping(_ context.Context, uuid string) (*driver.EventSourceMappingInfo, error) {
	info, ok := m.mappings[uuid]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListEventSourceMappings(_ context.Context, functionName string) ([]driver.EventSourceMappingInfo, error) {
	var result []driver.EventSourceMappingInfo

	for _, info := range m.mappings {
		if functionName == "" || info.FunctionName == functionName {
			result = append(result, *info)
		}
	}

	return result, nil
}

func (m *mockDriver) UpdateEventSourceMapping(
	_ context.Context, uuid string, config driver.EventSourceMappingConfig,
) (*driver.EventSourceMappingInfo, error) {
	info, ok := m.mappings[uuid]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	if config.FunctionName != "" {
		info.FunctionName = config.FunctionName
	}

	info.Enabled = config.Enabled

	if config.Enabled {
		info.State = "Enabled"
	} else {
		info.State = "Disabled"
	}

	return info, nil
}

func newTestServerless(opts ...Option) *Serverless {
	return NewServerless(newMockDriver(), opts...)
}

func setupFunction(t *testing.T, s *Serverless, name string) {
	t.Helper()

	ctx := context.Background()
	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: name, Runtime: "go1.x", Handler: "main"})
	require.NoError(t, err)
}

func TestNewServerless(t *testing.T) {
	s := newTestServerless()
	require.NotNil(t, s)
	require.NotNil(t, s.driver)
}

func TestCreateFunction(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "my-func", Runtime: "go1.x"})
		require.NoError(t, err)
		assert.Equal(t, "my-func", info.Name)
		assert.Equal(t, "Active", info.State)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := s.CreateFunction(ctx, driver.FunctionConfig{})
		require.Error(t, err)
	})
}

func TestDeleteFunction(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "del-func")

	t.Run("success", func(t *testing.T) {
		err := s.DeleteFunction(ctx, "del-func")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := s.DeleteFunction(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetFunction(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "get-func")

	t.Run("success", func(t *testing.T) {
		info, err := s.GetFunction(ctx, "get-func")
		require.NoError(t, err)
		assert.Equal(t, "get-func", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.GetFunction(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListFunctions(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	funcs, err := s.ListFunctions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(funcs))

	setupFunction(t, s, "func-a")
	setupFunction(t, s, "func-b")

	funcs, err = s.ListFunctions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(funcs))
}

func TestUpdateFunction(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "upd-func")

	t.Run("success", func(t *testing.T) {
		info, err := s.UpdateFunction(ctx, "upd-func", driver.FunctionConfig{Runtime: "python3.9"})
		require.NoError(t, err)
		assert.Equal(t, "python3.9", info.Runtime)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.UpdateFunction(ctx, "nonexistent", driver.FunctionConfig{Runtime: "python3.9"})
		require.Error(t, err)
	})
}

func TestInvoke(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "inv-func")

	t.Run("success", func(t *testing.T) {
		out, err := s.Invoke(ctx, driver.InvokeInput{FunctionName: "inv-func", Payload: []byte("hello")})
		require.NoError(t, err)
		assert.Equal(t, 200, out.StatusCode)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.Invoke(ctx, driver.InvokeInput{FunctionName: "nonexistent"})
		require.Error(t, err)
	})
}

func TestRegisterHandler(t *testing.T) {
	md := newMockDriver()
	s := NewServerless(md)

	s.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})
	assert.NotNil(t, md.handlers["my-func"])
}

func TestPublishVersion(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "ver-func")

	t.Run("success", func(t *testing.T) {
		v, err := s.PublishVersion(ctx, "ver-func", "first release")
		require.NoError(t, err)
		assert.Equal(t, "ver-func", v.FunctionName)
		assert.Equal(t, "1", v.Version)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.PublishVersion(ctx, "nonexistent", "desc")
		require.Error(t, err)
	})
}

func TestListVersions(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "lv-func")

	_, err := s.PublishVersion(ctx, "lv-func", "v1")
	require.NoError(t, err)

	_, err = s.PublishVersion(ctx, "lv-func", "v2")
	require.NoError(t, err)

	versions, err := s.ListVersions(ctx, "lv-func")
	require.NoError(t, err)
	assert.Equal(t, 2, len(versions))
}

func TestCreateAlias(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "alias-func")

	t.Run("success", func(t *testing.T) {
		a, err := s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "alias-func", Name: "prod", FunctionVersion: "1"})
		require.NoError(t, err)
		assert.Equal(t, "prod", a.Name)
		assert.Equal(t, "1", a.FunctionVersion)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "nonexistent", Name: "prod"})
		require.Error(t, err)
	})
}

func TestUpdateAlias(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "ua-func")

	_, err := s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "ua-func", Name: "prod", FunctionVersion: "1"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		a, err := s.UpdateAlias(ctx, driver.AliasConfig{FunctionName: "ua-func", Name: "prod", FunctionVersion: "2"})
		require.NoError(t, err)
		assert.Equal(t, "2", a.FunctionVersion)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.UpdateAlias(ctx, driver.AliasConfig{FunctionName: "ua-func", Name: "nonexistent"})
		require.Error(t, err)
	})
}

func TestDeleteAlias(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "da-func")

	_, err := s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "da-func", Name: "prod", FunctionVersion: "1"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := s.DeleteAlias(ctx, "da-func", "prod")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := s.DeleteAlias(ctx, "da-func", "nonexistent")
		require.Error(t, err)
	})
}

func TestGetAlias(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "ga-func")

	_, err := s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "ga-func", Name: "prod", FunctionVersion: "1"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		a, err := s.GetAlias(ctx, "ga-func", "prod")
		require.NoError(t, err)
		assert.Equal(t, "prod", a.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.GetAlias(ctx, "ga-func", "nonexistent")
		require.Error(t, err)
	})
}

func TestListAliases(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "la-func")

	_, err := s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "la-func", Name: "prod", FunctionVersion: "1"})
	require.NoError(t, err)

	_, err = s.CreateAlias(ctx, driver.AliasConfig{FunctionName: "la-func", Name: "staging", FunctionVersion: "2"})
	require.NoError(t, err)

	aliases, err := s.ListAliases(ctx, "la-func")
	require.NoError(t, err)
	assert.Equal(t, 2, len(aliases))
}

func TestPublishLayerVersion(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		lv, err := s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "my-layer", Description: "test"})
		require.NoError(t, err)
		assert.Equal(t, "my-layer", lv.Name)
		assert.Equal(t, 1, lv.Version)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := s.PublishLayerVersion(ctx, driver.LayerConfig{})
		require.Error(t, err)
	})
}

func TestGetLayerVersion(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	lv, err := s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "gl-layer"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		got, err := s.GetLayerVersion(ctx, "gl-layer", lv.Version)
		require.NoError(t, err)
		assert.Equal(t, "gl-layer", got.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.GetLayerVersion(ctx, "gl-layer", 999)
		require.Error(t, err)
	})
}

func TestListLayerVersions(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	_, err := s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "llv-layer"})
	require.NoError(t, err)

	_, err = s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "llv-layer"})
	require.NoError(t, err)

	versions, err := s.ListLayerVersions(ctx, "llv-layer")
	require.NoError(t, err)
	assert.Equal(t, 2, len(versions))
}

func TestDeleteLayerVersion(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	lv, err := s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "dlv-layer"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := s.DeleteLayerVersion(ctx, "dlv-layer", lv.Version)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := s.DeleteLayerVersion(ctx, "dlv-layer", 999)
		require.Error(t, err)
	})
}

func TestListLayers(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	_, err := s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer-a"})
	require.NoError(t, err)

	_, err = s.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer-b"})
	require.NoError(t, err)

	layers, err := s.ListLayers(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(layers))
}

func TestPutFunctionConcurrency(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "conc-func")

	t.Run("success", func(t *testing.T) {
		err := s.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{FunctionName: "conc-func", ReservedConcurrentExecutions: 10})
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := s.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{FunctionName: "nonexistent", ReservedConcurrentExecutions: 10})
		require.Error(t, err)
	})
}

func TestGetFunctionConcurrency(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "gc-func")

	err := s.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{FunctionName: "gc-func", ReservedConcurrentExecutions: 5})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		c, err := s.GetFunctionConcurrency(ctx, "gc-func")
		require.NoError(t, err)
		assert.Equal(t, 5, c.ReservedConcurrentExecutions)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.GetFunctionConcurrency(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDeleteFunctionConcurrency(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()
	setupFunction(t, s, "dc-func")

	err := s.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{FunctionName: "dc-func", ReservedConcurrentExecutions: 5})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := s.DeleteFunctionConcurrency(ctx, "dc-func")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := s.DeleteFunctionConcurrency(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestServerlessWithRecorder(t *testing.T) {
	rec := recorder.New()
	s := newTestServerless(WithRecorder(rec))
	ctx := context.Background()

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "rec-func", Runtime: "go1.x"})
	require.NoError(t, err)

	_, err = s.GetFunction(ctx, "rec-func")
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 2)

	createCalls := rec.CallCountFor("serverless", "CreateFunction")
	assert.Equal(t, 1, createCalls)

	getCalls := rec.CallCountFor("serverless", "GetFunction")
	assert.Equal(t, 1, getCalls)
}

func TestServerlessWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	s := newTestServerless(WithRecorder(rec))
	ctx := context.Background()

	_, _ = s.GetFunction(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last)
	assert.NotNil(t, last.Error)
}

func TestServerlessWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestServerless(WithMetrics(mc))
	ctx := context.Background()

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "met-func", Runtime: "go1.x"})
	require.NoError(t, err)

	_, err = s.GetFunction(ctx, "met-func")
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 2)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 2)
}

func TestServerlessWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestServerless(WithMetrics(mc))
	ctx := context.Background()

	_, _ = s.GetFunction(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestServerlessWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestServerless(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("serverless", "CreateFunction", injectedErr, inject.Always{})

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "fail-func"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestServerlessWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	s := newTestServerless(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("serverless", "GetFunction", injectedErr, inject.Always{})

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "inj-func", Runtime: "go1.x"})
	require.NoError(t, err)

	_, err = s.GetFunction(ctx, "inj-func")
	require.Error(t, err)

	getCalls := rec.CallsFor("serverless", "GetFunction")
	assert.Equal(t, 1, len(getCalls))
	assert.NotNil(t, getCalls[0].Error)
}

func TestServerlessWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestServerless(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("serverless", "CreateFunction", injectedErr, inject.Always{})

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "test"})
	require.Error(t, err)

	inj.Remove("serverless", "CreateFunction")

	_, err = s.CreateFunction(ctx, driver.FunctionConfig{Name: "test"})
	require.NoError(t, err)
}

func TestServerlessWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	s := newTestServerless(WithLatency(latency))
	ctx := context.Background()

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "lat-func", Runtime: "go1.x"})
	require.NoError(t, err)

	info, err := s.GetFunction(ctx, "lat-func")
	require.NoError(t, err)
	assert.Equal(t, "lat-func", info.Name)
}

func TestServerlessAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	s := newTestServerless(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := s.CreateFunction(ctx, driver.FunctionConfig{Name: "all-opts", Runtime: "go1.x"})
	require.NoError(t, err)

	_, err = s.GetFunction(ctx, "all-opts")
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestCreateEventSourceMapping(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
			FunctionName:   "my-func",
			EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:my-queue",
			Enabled:        true,
		})
		require.NoError(t, err)
		assert.Equal(t, "my-func", info.FunctionName)
		assert.Equal(t, "Enabled", info.State)
	})

	t.Run("missing function name", func(t *testing.T) {
		_, err := s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
			EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:my-queue",
		})
		require.Error(t, err)
	})
}

func TestDeleteEventSourceMapping(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	info, err := s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		FunctionName:   "del-func",
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:del-queue",
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := s.DeleteEventSourceMapping(ctx, info.UUID)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := s.DeleteEventSourceMapping(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetEventSourceMapping(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	created, err := s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		FunctionName:   "get-func",
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:get-queue",
		Enabled:        true,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, err := s.GetEventSourceMapping(ctx, created.UUID)
		require.NoError(t, err)
		assert.Equal(t, "get-func", info.FunctionName)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.GetEventSourceMapping(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListEventSourceMappings(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	_, err := s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		FunctionName:   "list-func-a",
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:queue-a",
	})
	require.NoError(t, err)

	_, err = s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		FunctionName:   "list-func-b",
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:queue-b",
	})
	require.NoError(t, err)

	mappings, err := s.ListEventSourceMappings(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 2, len(mappings))

	mappings, err = s.ListEventSourceMappings(ctx, "list-func-a")
	require.NoError(t, err)
	assert.Equal(t, 1, len(mappings))
}

func TestUpdateEventSourceMapping(t *testing.T) {
	s := newTestServerless()
	ctx := context.Background()

	created, err := s.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		FunctionName:   "upd-func",
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:upd-queue",
		Enabled:        false,
	})
	require.NoError(t, err)
	assert.Equal(t, "Disabled", created.State)

	t.Run("enable mapping", func(t *testing.T) {
		info, err := s.UpdateEventSourceMapping(ctx, created.UUID, driver.EventSourceMappingConfig{
			Enabled: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "Enabled", info.State)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.UpdateEventSourceMapping(ctx, "nonexistent", driver.EventSourceMappingConfig{})
		require.Error(t, err)
	})
}
