package lambda

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/funcengine"
)

var _ driver.Serverless = (*Mock)(nil)

// initialVersion is the starting version number for published versions.
const initialVersion = 1

const (
	defaultBatchSize = 10
	stateEnabled     = "Enabled"
	stateDisabled    = "Disabled"
	timeFormat       = "2006-01-02T15:04:05Z"
)

// AWS Lambda create-time defaults applied when the client omits the field:
// MemorySize defaults to 128 MB and Timeout to 3 seconds. See
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateFunction.html
const (
	defaultMemoryMB    = 128
	defaultTimeoutSecs = 3
)

type versionData struct {
	config     driver.FunctionConfig
	version    string
	codeSHA    string
	revisionID string
	createdAt  string
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
	policy       map[string]driver.PermissionStatement
	urlConfig    *driver.FunctionURLConfig // Lambda Function URL, nil until created
}

// Mock is an in-memory mock implementation of AWS Lambda.
type Mock struct {
	funcs      *memstore.Store[funcData]
	layers     *memstore.Store[*layerData]
	mappings   *memstore.Store[*driver.EventSourceMappingInfo]
	opts       *config.Options
	handlersMu sync.RWMutex
	handlers   map[string]driver.HandlerFunc
	monitoring mondriver.Monitoring
	mu         sync.Mutex // guards PublishVersion read-modify-write on funcData
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
		mappings: memstore.New[*driver.EventSourceMappingInfo](),
		opts:     opts,
		handlers: make(map[string]driver.HandlerFunc),
	}
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateFunction(ctx context.Context, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if _, ok := m.funcs.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}

	// AWS applies documented create-time defaults when the client omits these:
	// MemorySize -> 128 MB, Timeout -> 3 s. Terraform/CDK read these back and see
	// a perpetual diff if the response reports 0.
	if cfg.Memory == 0 {
		cfg.Memory = defaultMemoryMB
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeoutSecs
	}

	arn := idgen.AWSARN("lambda", m.opts.Region, m.opts.AccountID, "function:"+cfg.Name)
	info := driver.FunctionInfo{
		Name: cfg.Name, ARN: arn, Runtime: cfg.Runtime, Handler: cfg.Handler,
		Role: cfg.Role, Description: cfg.Description,
		Memory: cfg.Memory, Timeout: cfg.Timeout, State: "Active",
		Environment: maps.Clone(cfg.Environment), Tags: maps.Clone(cfg.Tags),
		LastModified: m.opts.Clock.Now().UTC().Format(time.RFC3339),
		CodeSHA256:   codeHash(cfg.Code), CodeSize: int64(len(cfg.Code)),
		Version: latestVersion, RevisionID: newRevisionID(),
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

	for k := range all {
		infos = append(infos, all[k].info)
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
	// Every update — configuration or code — mints a new revision, matching the
	// RevisionId Terraform reads to detect drift.
	info.RevisionID = newRevisionID()

	if len(cfg.Code) > 0 {
		info.CodeSHA256 = codeHash(cfg.Code)
		info.CodeSize = int64(len(cfg.Code))
	}

	fd.info = info

	// A code update re-deploys to the engine using the post-merge runtime/handler
	// so the function runs the new code, not the stale deployment.
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

	if h == nil && fd.engineBacked {
		return m.invokeEngine(ctx, input, dims)
	}

	if h == nil {
		// The emulator can't execute an uploaded zip (arbitrary Python/Node/
		// etc.), so with no Go handler registered we return a successful stub
		// that echoes the request payload rather than a FunctionError. This
		// lets users exercise invoke control flow (wiring, permissions,
		// event-source mappings) without a real runtime. Register a handler via
		// RegisterHandler to run real logic.
		m.emitMetric(ctx, "Invocations", 1, dims)
		m.emitMetric(ctx, "Duration", 1.0, dims)

		payload := input.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}

		return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
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

// invokeEngine runs a function whose code was deployed to the configured
// FunctionEngine and records the same metrics as a real handler invocation. A
// handler that raised is reported via out.Error (HTTP stays 200), matching real
// Lambda's X-Amz-Function-Error semantics.
func (m *Mock) invokeEngine(ctx context.Context, input driver.InvokeInput, dims map[string]string) (*driver.InvokeOutput, error) {
	out, err := funcengine.Invoke(ctx, m.opts.FunctionEngine, input.FunctionName, input.Payload)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "invoke function %s: %v", input.FunctionName, err)
	}

	m.emitMetric(ctx, "Invocations", 1, dims)

	if out.Error != "" {
		m.emitMetric(ctx, "Errors", 1, dims)

		return out, nil
	}

	m.emitMetric(ctx, "Duration", 1.0, dims)
	m.emitMetric(ctx, "ConcurrentExecutions", 1, dims)

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

	if cfg.Role != "" {
		info.Role = cfg.Role
	}

	if cfg.Description != "" {
		info.Description = cfg.Description
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

// codeHash returns the base64-encoded SHA-256 of the deployment package, the
// same CodeSha256 shape real Lambda returns and Terraform compares against its
// locally computed source_code_hash.
func codeHash(code []byte) string {
	hash := sha256.Sum256(code)

	return base64.StdEncoding.EncodeToString(hash[:])
}

// newRevisionID mints a random UUID-shaped revision identifier. Lambda changes
// the RevisionId on every configuration or code mutation.
func newRevisionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not expected; fall back to a zero-value UUID so
		// callers still get a well-formed (if non-unique) revision id.
		return "00000000-0000-4000-8000-000000000000"
	}

	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	h := hex.EncodeToString(b[:])

	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}
