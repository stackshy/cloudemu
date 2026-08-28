// Package functions provides an in-memory mock implementation of Azure Functions.
package functions

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/funcengine"
)

// Compile-time check that Mock implements driver.Serverless.
var _ driver.Serverless = (*Mock)(nil)

// initialVersion is the starting version number for published versions.
const initialVersion = 1

const (
	defaultBatchSize = 10
	stateEnabled     = "Enabled"
	stateDisabled    = "Disabled"
	timeFormat       = "2006-01-02T15:04:05Z"
)

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
	info         driver.FunctionInfo
	handler      driver.HandlerFunc
	engineBacked bool // real code deployed to the configured FunctionEngine
	versions     []*versionData
	nextVersion  int
	aliases      *memstore.Store[*aliasData]
	concurrency  *driver.ConcurrencyConfig
}

// Mock is an in-memory mock implementation of Azure Functions.
type Mock struct {
	funcs      *memstore.Store[funcData]
	layers     *memstore.Store[*layerData]
	mappings   *memstore.Store[*driver.EventSourceMappingInfo]
	plans      *memstore.Store[*AppServicePlan]
	sites      *memstore.Store[*SiteMeta]
	opts       *config.Options
	handlersMu sync.RWMutex
	handlers   map[string]driver.HandlerFunc
	monitoring mondriver.Monitoring
	logs       logdriver.Logging // optional: Log Analytics sink for invocation logs
	mu         sync.Mutex        // guards PublishVersion read-modify-write on funcData
	sitesMu    sync.RWMutex      // guards SiteMeta read-modify-write
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetLogSink wires the Log Analytics target that Invoke writes each
// invocation's execution log lines (and any captured stdout/stderr) into.
// Safe to leave unset — invocation-log surfacing is then skipped.
func (m *Mock) SetLogSink(l logdriver.Logging) {
	m.logs = l
}

func (m *Mock) emitMetric(functionName string, metrics map[string]float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, 0, len(metrics))

	for name, value := range metrics {
		data = append(data, mondriver.MetricDatum{
			Namespace:  "Microsoft.Web/sites",
			MetricName: name,
			Value:      value,
			Unit:       "None",
			Dimensions: map[string]string{"functionName": functionName},
			Timestamp:  now,
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// New creates a new Azure Functions mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		funcs:    memstore.New[funcData](),
		layers:   memstore.New[*layerData](),
		mappings: memstore.New[*driver.EventSourceMappingInfo](),
		plans:    memstore.New[*AppServicePlan](),
		sites:    memstore.New[*SiteMeta](),
		opts:     opts,
		handlers: make(map[string]driver.HandlerFunc),
	}
}

// CreateFunction creates a new Azure Function.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateFunction(ctx context.Context, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if _, ok := m.funcs.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	resourceID := idgen.AzureID(m.opts.AccountID, "cloudemu-rg", "Microsoft.Web", "sites", cfg.Name)
	info := driver.FunctionInfo{
		Name: cfg.Name, ARN: resourceID, Runtime: cfg.Runtime, Handler: cfg.Handler,
		Memory: cfg.Memory, Timeout: cfg.Timeout, State: "Active",
		Environment: maps.Clone(cfg.Environment), Tags: maps.Clone(cfg.Tags),
		LastModified: m.opts.Clock.Now().UTC().Format(time.RFC3339),
	}

	m.handlersMu.RLock()
	h := m.handlers[cfg.Name]
	m.handlersMu.RUnlock()

	engineBacked, err := funcengine.Deploy(ctx, m.opts.FunctionEngine, &cfg)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", cfg.Name, err)
	}

	m.funcs.Set(cfg.Name, funcData{
		info: info, handler: h, engineBacked: engineBacked,
		nextVersion: initialVersion,
		aliases:     memstore.New[*aliasData](),
	})

	result := info

	return &result, nil
}

// DeleteFunction deletes an Azure Function by name.
func (m *Mock) DeleteFunction(ctx context.Context, name string) error {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	if fd.engineBacked {
		if err := funcengine.Remove(ctx, m.opts.FunctionEngine, name); err != nil {
			return cerrors.Newf(cerrors.Internal, "remove function %s: %v", name, err)
		}
	}

	m.funcs.Delete(name)

	return nil
}

// GetFunction retrieves an Azure Function by name.
func (m *Mock) GetFunction(_ context.Context, name string) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	info := fd.info
	info.Environment = maps.Clone(info.Environment)
	info.Tags = maps.Clone(info.Tags)

	return &info, nil
}

// ListFunctions lists all Azure Functions.
func (m *Mock) ListFunctions(_ context.Context) ([]driver.FunctionInfo, error) {
	all := m.funcs.All()
	infos := make([]driver.FunctionInfo, 0, len(all))

	for i := range all {
		infos = append(infos, all[i].info)
	}

	return infos, nil
}

// UpdateFunction updates an existing Azure Function's configuration.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateFunction(ctx context.Context, name string, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	info := fd.info
	applyConfigUpdates(&info, cfg)
	info.LastModified = m.opts.Clock.Now().UTC().Format(time.RFC3339)
	fd.info = info

	// A code update (e.g. a Kudu zipdeploy PUT) re-deploys to the engine using
	// the post-merge runtime/handler so the function runs the new code, not the
	// stale deployment.
	if len(cfg.Code) > 0 {
		backed, err := funcengine.Deploy(ctx, m.opts.FunctionEngine, &driver.FunctionConfig{
			Name: name, Runtime: info.Runtime, Handler: info.Handler,
			Code: cfg.Code, Environment: info.Environment, Timeout: info.Timeout,
		})
		if err != nil {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", name, err)
		}

		fd.engineBacked = backed
	}

	m.funcs.Set(name, fd)

	result := info

	return &result, nil
}

// Invoke invokes an Azure Function with the given input.
func (m *Mock) Invoke(ctx context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	fd, ok := m.funcs.Get(input.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", input.FunctionName)
	}

	h := fd.handler
	if h == nil {
		m.handlersMu.RLock()
		h = m.handlers[input.FunctionName]
		m.handlersMu.RUnlock()
	}

	if h == nil && fd.engineBacked {
		return m.invokeEngine(ctx, input)
	}

	if h == nil {
		// The emulator can't execute uploaded function code, so with no Go
		// handler registered we return a successful stub echoing the request
		// payload rather than a FunctionError — mirroring the AWS Lambda
		// provider so identical cross-provider tests behave the same.
		m.emitMetric(input.FunctionName, map[string]float64{
			"FunctionExecutionCount": 1, "FunctionExecutionUnits": 1,
		})
		m.surfaceInvokeLogs(ctx, input.FunctionName, "", "")

		payload := input.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}

		return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
	}

	payload, err := h(ctx, input.Payload)
	if err != nil {
		m.emitMetric(input.FunctionName, map[string]float64{
			"FunctionExecutionCount": 1, "FunctionExecutionUnits": 1, "FunctionErrors": 1,
		})
		m.surfaceInvokeLogs(ctx, input.FunctionName, "", err.Error())

		return &driver.InvokeOutput{StatusCode: 500, Error: err.Error()}, nil
	}

	m.emitMetric(input.FunctionName, map[string]float64{
		"FunctionExecutionCount": 1, "FunctionExecutionUnits": 1,
	})
	m.surfaceInvokeLogs(ctx, input.FunctionName, "", "")

	return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
}

// InvokeExternal asynchronously invokes the named function app with payload,
// used for cross-service delivery such as Event Grid -> AzureFunction where the
// source only knows the destination app. A missing function is a no-op (the
// subscription may point at an app this emulator never created), mirroring
// lambda.InvokeExternal. ctx carries the re-entrant delivery depth (see
// internal/recursionguard): a function that re-publishes an event that routes
// back to itself would otherwise recurse unbounded, so once the depth reaches
// recursionguard.MaxDepth further invocation is dropped.
func (m *Mock) InvokeExternal(ctx context.Context, name string, payload []byte) error {
	if _, ok := m.funcs.Get(name); !ok {
		return nil
	}

	depth := recursionguard.Depth(ctx)
	if depth >= recursionguard.MaxDepth {
		return nil
	}

	ctx = recursionguard.WithDepth(ctx, depth+1)

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: name, Payload: payload, InvokeType: "Event"})
	if err != nil {
		return err
	}

	if out != nil && out.Error != "" {
		return cerrors.Newf(cerrors.Internal, "function %s returned an error: %s", name, out.Error)
	}

	return nil
}

// invokeEngine runs a function whose code was deployed to the configured
// FunctionEngine. A handler that raised is surfaced via out.Error, which the
// wire handler renders as Azure's 500 + plain-text error body; a Go error means
// the engine itself failed to run the code.
func (m *Mock) invokeEngine(ctx context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	out, err := funcengine.Invoke(ctx, m.opts.FunctionEngine, input.FunctionName, input.Payload)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "invoke function %s: %v", input.FunctionName, err)
	}

	if out.Error != "" {
		m.emitMetric(input.FunctionName, map[string]float64{
			"FunctionExecutionCount": 1, "FunctionExecutionUnits": 1, "FunctionErrors": 1,
		})
		m.surfaceInvokeLogs(ctx, input.FunctionName, out.Logs, out.Error)

		return out, nil
	}

	m.emitMetric(input.FunctionName, map[string]float64{
		"FunctionExecutionCount": 1, "FunctionExecutionUnits": 1,
	})
	m.surfaceInvokeLogs(ctx, input.FunctionName, out.Logs, "")

	return out, nil
}

// RegisterHandler registers a handler function for an Azure Function by name.
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
		info.Environment = maps.Clone(cfg.Environment)
	}

	if cfg.Tags != nil {
		info.Tags = maps.Clone(cfg.Tags)
	}
}

func codeSHA(info *driver.FunctionInfo) string {
	data := fmt.Sprintf("%s:%s:%s", info.Name, info.Handler, info.Runtime)
	hash := sha256.Sum256([]byte(data))

	return fmt.Sprintf("%x", hash)
}
