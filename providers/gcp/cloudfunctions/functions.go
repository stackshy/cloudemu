// Package cloudfunctions provides an in-memory mock implementation of Google Cloud Functions.
package cloudfunctions

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
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/funcengine"
)

// Compile-time check that Mock implements driver.Serverless.
var _ driver.Serverless = (*Mock)(nil)

// initialVersion is the starting generation number for published versions.
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

// Mock is an in-memory mock implementation of Google Cloud Functions.
type Mock struct {
	funcs      *memstore.Store[funcData]
	layers     *memstore.Store[*layerData]
	mappings   *memstore.Store[*driver.EventSourceMappingInfo]
	opts       *config.Options
	handlersMu sync.RWMutex
	handlers   map[string]driver.HandlerFunc
	monitoring mondriver.Monitoring
	logs       logdriver.Logging // optional: Cloud Logging sink for invocation logs
	mu         sync.Mutex        // guards PublishVersion read-modify-write on funcData
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetLogSink wires the Cloud Logging target that Invoke writes each
// invocation's execution log lines (and any captured stdout/stderr) into.
// Safe to leave unset — invocation-log surfacing is then skipped.
func (m *Mock) SetLogSink(l logdriver.Logging) {
	m.logs = l
}

//nolint:unparam // value kept as parameter for API consistency with other service emitMetric helpers.
func (m *Mock) emitMetric(ctx context.Context, metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(ctx, []mondriver.MetricDatum{
		{
			Namespace:  "cloudfunctions.googleapis.com",
			MetricName: metricName,
			Value:      value,
			Unit:       "None",
			Dimensions: dims,
			Timestamp:  m.opts.Clock.Now(),
		},
	})
}

// New creates a new Cloud Functions mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		funcs:    memstore.New[funcData](),
		layers:   memstore.New[*layerData](),
		mappings: memstore.New[*driver.EventSourceMappingInfo](),
		opts:     opts,
		handlers: make(map[string]driver.HandlerFunc),
	}
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateFunction(ctx context.Context, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	// Fast-path check: avoids the deploy call for the common (non-racing)
	// duplicate-name case, but does not by itself prevent two concurrent
	// creates of the same name both passing — that guard is the SetIfAbsent
	// below, which is the atomic compare-and-set under the store lock.
	if _, ok := m.funcs.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	arn := idgen.GCPID(m.opts.ProjectID, "functions", cfg.Name)

	env := make(map[string]string, len(cfg.Environment))
	for k, v := range cfg.Environment {
		env[k] = v
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.FunctionInfo{
		Name: cfg.Name, ARN: arn, Runtime: cfg.Runtime, Handler: cfg.Handler,
		Memory: cfg.Memory, Timeout: cfg.Timeout, State: "ACTIVE",
		Environment: env, Tags: tags,
		LastModified: m.opts.Clock.Now().UTC().Format(time.RFC3339),
	}

	m.handlersMu.RLock()
	h := m.handlers[cfg.Name]
	m.handlersMu.RUnlock()

	fd := funcData{
		info: info, handler: h,
		nextVersion: initialVersion,
		aliases:     memstore.New[*aliasData](),
	}

	// Claim the name atomically BEFORE deploying to the engine. funcengine's
	// Deploy/Remove are keyed by name only (no per-create handle), so if two
	// concurrent creates both deployed first and then raced SetIfAbsent, the
	// losing racer's "cleanup" Remove(name) would tear down whatever is
	// currently registered under that name — which can be the WINNER's
	// deployment, leaving it engineBacked but with no live deployment behind
	// it. Reserving the name first guarantees only the actual owner of the
	// name ever calls Deploy/Remove for it, so a losing racer touches no
	// engine state at all.
	if !m.funcs.SetIfAbsent(cfg.Name, fd) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	engineBacked, err := funcengine.Deploy(ctx, m.opts.FunctionEngine, &cfg)
	if err != nil {
		// We own the name (SetIfAbsent succeeded above): roll back our own
		// reservation rather than leaving a store entry with no code deployed.
		m.funcs.Delete(cfg.Name)

		return nil, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", cfg.Name, err)
	}

	if engineBacked && !m.funcs.Update(cfg.Name, func(cur funcData) funcData {
		cur.engineBacked = true

		return cur
	}) {
		// The entry was deleted concurrently (e.g. a racing DeleteFunction) after
		// we claimed the name but before the deploy landed. We are still the
		// deploy's owner, so best-effort tear it down rather than leaking it.
		_ = funcengine.Remove(ctx, m.opts.FunctionEngine, cfg.Name)
	}

	result := info

	return &result, nil
}

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

func (m *Mock) ListFunctions(_ context.Context) ([]driver.FunctionInfo, error) {
	all := m.funcs.All()
	infos := make([]driver.FunctionInfo, 0, len(all))

	for i := range all {
		infos = append(infos, all[i].info)
	}

	return infos, nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateFunction(ctx context.Context, name string, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	info := fd.info
	applyConfigUpdates(&info, cfg)
	info.LastModified = m.opts.Clock.Now().UTC().Format(time.RFC3339)

	// A code update re-deploys to the engine using the post-merge runtime/handler
	// so the function runs the new code, not the stale deployment. This external
	// call happens outside the store lock (Deploy may be slow), based on the fd
	// snapshot read above; the write-back below is what must be atomic.
	backed := fd.engineBacked
	deployed := false

	if len(cfg.Code) > 0 {
		var err error

		backed, err = funcengine.Deploy(ctx, m.opts.FunctionEngine, &driver.FunctionConfig{
			Name: name, Runtime: info.Runtime, Handler: info.Handler, Framework: cfg.Framework,
			Code: cfg.Code, Environment: info.Environment, Timeout: info.Timeout,
		})
		if err != nil {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "deploy function %s: %v", name, err)
		}

		deployed = true
	}

	// Update applies the merge under the store's write lock, so a concurrent
	// DeleteFunction (Get+Delete) can't be interleaved between our read and this
	// write: either the delete lands first and this reports NotFound (below),
	// leaving the entry gone, or this write lands first and the delete then
	// removes it cleanly — never a stale resurrection of a deleted entry.
	ok = m.funcs.Update(name, func(cur funcData) funcData {
		cur.info = info
		if deployed {
			cur.engineBacked = backed
		}

		return cur
	})
	if !ok {
		// The entry was deleted concurrently between our read and this write; do
		// not resurrect it. Best-effort tear down the engine deployment we just
		// made, mirroring DeleteFunction's own cleanup.
		if deployed && backed {
			_ = funcengine.Remove(ctx, m.opts.FunctionEngine, name)
		}

		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	result := info

	return &result, nil
}

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
		noHandlerDims := map[string]string{"function_name": input.FunctionName}
		m.emitMetric(ctx, "function/execution_count", 1, noHandlerDims)
		m.emitMetric(ctx, "function/execution_times", 1, noHandlerDims)
		m.surfaceInvokeLogs(ctx, input.FunctionName, "", "")

		payload := input.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}

		return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
	}

	dims := map[string]string{"function_name": input.FunctionName}

	payload, err := h(ctx, input.Payload)
	if err != nil {
		m.emitMetric(ctx, "function/execution_count", 1, dims)
		m.emitMetric(ctx, "function/execution_times", 1, dims)
		m.emitMetric(ctx, "function/error_count", 1, dims)
		m.surfaceInvokeLogs(ctx, input.FunctionName, "", err.Error())

		return &driver.InvokeOutput{StatusCode: 500, Error: err.Error()}, nil
	}

	m.emitMetric(ctx, "function/execution_count", 1, dims)
	m.emitMetric(ctx, "function/execution_times", 1, dims)
	m.surfaceInvokeLogs(ctx, input.FunctionName, "", "")

	return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
}

// invokeEngine runs a function whose code was deployed to the configured
// FunctionEngine and records the same metrics as a real handler invocation. A
// handler that raised is reported via out.Error (HTTP stays 200), mirroring the
// AWS Lambda provider.
func (m *Mock) invokeEngine(ctx context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	dims := map[string]string{"function_name": input.FunctionName}

	out, err := funcengine.Invoke(ctx, m.opts.FunctionEngine, input.FunctionName, input.Payload)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "invoke function %s: %v", input.FunctionName, err)
	}

	m.emitMetric(ctx, "function/execution_count", 1, dims)
	m.emitMetric(ctx, "function/execution_times", 1, dims)

	if out.Error != "" {
		m.emitMetric(ctx, "function/error_count", 1, dims)
	}

	m.surfaceInvokeLogs(ctx, input.FunctionName, out.Logs, out.Error)

	return out, nil
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
