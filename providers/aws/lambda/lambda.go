package lambda

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/serverless/driver"
)

var _ driver.Serverless = (*Mock)(nil)

// initialVersion is the starting version number for published versions.
const initialVersion = 1

type versionData struct {
	config    driver.FunctionConfig
	version   string
	codeSHA   string
	createdAt string
}

type aliasData struct {
	alias driver.Alias
}

type layerData struct {
	versions *memstore.Store[*driver.LayerVersion]
	nextVer  int
}

type funcData struct {
	info        driver.FunctionInfo
	handler     driver.HandlerFunc
	versions    []*versionData
	nextVersion int
	aliases     *memstore.Store[*aliasData]
	concurrency *driver.ConcurrencyConfig
}

// Mock is an in-memory mock implementation of AWS Lambda.
type Mock struct {
	funcs      *memstore.Store[funcData]
	layers     *memstore.Store[*layerData]
	opts       *config.Options
	handlersMu sync.RWMutex
	handlers   map[string]driver.HandlerFunc
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

//nolint:unparam // value is always 1 today but kept for future metrics like batch invocation counts.
func (m *Mock) emitMetric(ctx context.Context, metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(ctx, []mondriver.MetricDatum{{
		Namespace: "AWS/Lambda", MetricName: metricName, Value: value, Unit: "Count",
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new Lambda mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		funcs:    memstore.New[funcData](),
		layers:   memstore.New[*layerData](),
		opts:     opts,
		handlers: make(map[string]driver.HandlerFunc),
	}
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateFunction(_ context.Context, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if _, ok := m.funcs.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	arn := idgen.AWSARN("lambda", m.opts.Region, m.opts.AccountID, "function:"+cfg.Name)
	info := driver.FunctionInfo{
		Name: cfg.Name, ARN: arn, Runtime: cfg.Runtime, Handler: cfg.Handler,
		Memory: cfg.Memory, Timeout: cfg.Timeout, State: "Active",
		Environment: cfg.Environment, Tags: cfg.Tags,
		LastModified: time.Now().UTC().Format(time.RFC3339),
	}

	m.handlersMu.RLock()
	h := m.handlers[cfg.Name]
	m.handlersMu.RUnlock()

	m.funcs.Set(cfg.Name, funcData{
		info: info, handler: h,
		nextVersion: initialVersion,
		aliases:     memstore.New[*aliasData](),
	})

	result := info

	return &result, nil
}

func (m *Mock) DeleteFunction(_ context.Context, name string) error {
	if !m.funcs.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	m.funcs.Delete(name)

	return nil
}

func (m *Mock) GetFunction(_ context.Context, name string) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	info := fd.info

	return &info, nil
}

func (m *Mock) ListFunctions(_ context.Context) ([]driver.FunctionInfo, error) {
	all := m.funcs.All()
	infos := make([]driver.FunctionInfo, 0, len(all))

	for k := range all {
		infos = append(infos, all[k].info)
	}

	return infos, nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateFunction(_ context.Context, name string, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	info := fd.info
	applyConfigUpdates(&info, cfg)
	info.LastModified = time.Now().UTC().Format(time.RFC3339)
	fd.info = info
	m.funcs.Set(name, fd)

	result := info

	return &result, nil
}

func (m *Mock) Invoke(ctx context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	fd, ok := m.funcs.Get(input.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", input.FunctionName)
	}

	dims := map[string]string{"FunctionName": input.FunctionName}

	h := fd.handler
	if h == nil {
		m.handlersMu.RLock()
		h = m.handlers[input.FunctionName]
		m.handlersMu.RUnlock()
	}

	if h == nil {
		m.emitMetric(ctx, "Invocations", 1, dims)
		m.emitMetric(ctx, "Errors", 1, dims)

		return &driver.InvokeOutput{StatusCode: 500, Error: "no handler registered"}, nil
	}

	payload, err := h(ctx, input.Payload)
	if err != nil {
		m.emitMetric(ctx, "Invocations", 1, dims)
		m.emitMetric(ctx, "Errors", 1, dims)

		return &driver.InvokeOutput{StatusCode: 500, Error: err.Error()}, nil
	}

	m.emitMetric(ctx, "Invocations", 1, dims)
	m.emitMetric(ctx, "Duration", 1.0, dims)
	m.emitMetric(ctx, "ConcurrentExecutions", 1, dims)

	return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
}

func (m *Mock) RegisterHandler(name string, handler driver.HandlerFunc) {
	m.handlersMu.Lock()
	m.handlers[name] = handler
	m.handlersMu.Unlock()

	if fd, ok := m.funcs.Get(name); ok {
		fd.handler = handler
		m.funcs.Set(name, fd)
	}
}

// applyConfigUpdates applies non-zero config fields to the function info.
//
//nolint:gocritic // hugeParam: config passed by value intentionally for snapshot semantics.
func applyConfigUpdates(info *driver.FunctionInfo, cfg driver.FunctionConfig) {
	if cfg.Runtime != "" {
		info.Runtime = cfg.Runtime
	}

	if cfg.Handler != "" {
		info.Handler = cfg.Handler
	}

	if cfg.Memory != 0 {
		info.Memory = cfg.Memory
	}

	if cfg.Timeout != 0 {
		info.Timeout = cfg.Timeout
	}

	if cfg.Environment != nil {
		info.Environment = cfg.Environment
	}

	if cfg.Tags != nil {
		info.Tags = cfg.Tags
	}
}

func codeSHA(info *driver.FunctionInfo) string {
	data := fmt.Sprintf("%s:%s:%s", info.Name, info.Handler, info.Runtime)
	hash := sha256.Sum256([]byte(data))

	return fmt.Sprintf("%x", hash)
}
