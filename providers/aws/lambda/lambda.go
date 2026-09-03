package lambda

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
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
	// tracingModePassThrough is the AWS Lambda default X-Ray tracing mode.
	tracingModePassThrough = "PassThrough"
	// invokeTypeDryRun is the X-Amz-Invocation-Type value that validates
	// parameters and permissions (including the Qualifier) without running the
	// function.
	invokeTypeDryRun = "DryRun"
	// invokeTypeEvent is the asynchronous (fire-and-forget) invocation type. A
	// failed Event invoke routes its event to the DLQ / OnFailure destination
	// after retries are exhausted; a successful one may route to OnSuccess.
	invokeTypeEvent = "Event"
	// dryRunStatusCode is the HTTP 204 No Content a successful DryRun reports.
	dryRunStatusCode = 204
)

// AWS Lambda create-time defaults applied when the client omits the field:
// MemorySize defaults to 128 MB and Timeout to 3 seconds. See
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateFunction.html
const (
	defaultMemoryMB    = 128
	defaultTimeoutSecs = 3
)

// AWS Lambda create-time range limits. MemorySize must be 128–10240 MB and
// Timeout must be 1–900 seconds; an out-of-range value is rejected with
// InvalidParameterValueException. The 10240 MB ceiling is the value the Lambda
// service actually enforces — the API reference's 32768 is only the wire-schema
// bound. See
// https://docs.aws.amazon.com/lambda/latest/dg/configuration-function-common.html
const (
	minMemoryMB    = 128
	maxMemoryMB    = 10240
	minTimeoutSecs = 1
	maxTimeoutSecs = 900
)

type versionData struct {
	config     driver.FunctionConfig
	version    string
	codeSHA    string
	revisionID string
	createdAt  string
}

type aliasData struct {
	mu    sync.Mutex // guards alias, mutated in place after the entity is stored
	alias driver.Alias
}

type layerData struct {
	// mu guards nextVer (the monotonic version counter's read-modify-write) and
	// permissions, the two pieces of layerData mutated in place after the entry
	// is stored — versions is itself a memstore.Store and is safe on its own.
	mu          sync.Mutex
	versions    *memstore.Store[*driver.LayerVersion]
	nextVer     int
	permissions map[int]*layerVersionPolicy
}

// layerVersionPolicy is one layer version's resource-based policy: the set of
// AddLayerVersionPermission statements keyed by StatementId, plus the revision
// id that changes on every add/remove (AWS's optimistic-concurrency guard for
// RevisionId-conditioned Add/RemoveLayerVersionPermission calls).
type layerVersionPolicy struct {
	statements map[string]driver.LayerPermissionStatement
	revisionID string
}

type funcData struct {
	info         driver.FunctionInfo
	handler      driver.HandlerFunc
	engineBacked bool // real code deployed to the configured FunctionEngine
	versions     []*versionData
	nextVersion  int
	aliases      *memstore.Store[*aliasData]
	concurrency  *driver.ConcurrencyConfig
	// policies is the resource-based policy keyed by qualifier (normalized via
	// policyKey: "" and "$LATEST" collapse to the unqualified function policy),
	// then by statement id. AWS keeps a separate policy per version/alias.
	policies map[string]map[string]driver.PermissionStatement
	// urlConfigs holds the Function URL config keyed by qualifier (normalized
	// via policyKey: "" and "$LATEST" collapse to the unqualified $LATEST URL,
	// an alias name is kept as-is), matching AWS's one-URL-per-(function,
	// qualifier) scoping. Real Lambda rejects a numbered-version qualifier
	// outright — see validateFunctionURLQualifier.
	urlConfigs map[string]*driver.FunctionURLConfig
	// awsConfig holds the AWS-only settings (VpcConfig/DeadLetterConfig/
	// TracingConfig) applied through the AWSConfigurable optional interface.
	awsConfig driver.AWSFunctionConfig
	// eventInvokeConfigs holds the asynchronous-invocation config keyed by
	// qualifier (normalized via policyKey: "" and "$LATEST" collapse to the
	// unqualified function config), matching AWS's per-version/alias scoping.
	eventInvokeConfigs map[string]driver.EventInvokeConfig
	// provisionedConcurrencyConfigs holds the provisioned-concurrency config
	// keyed by qualifier (a published version or alias name — unlike
	// eventInvokeConfigs, $LATEST/unqualified is rejected outright rather than
	// normalized, since real Lambda cannot attach provisioned concurrency to
	// the mutable $LATEST code).
	provisionedConcurrencyConfigs map[string]driver.ProvisionedConcurrencyConfig
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
	logs       logdriver.Logging // optional: CloudWatch Logs sink for invocation logs
	// dlqSQS / dlqSNS are the cross-service seams a failed asynchronous invoke
	// uses to route its event to the function's DeadLetterConfig queue/topic and
	// its OnFailure/OnSuccess async destinations. Both are optional: unset means
	// destination routing is silently skipped (library users without SQS/SNS are
	// unaffected). Delivery re-enters Lambda only through InvokeExternal, whose
	// recursion guard bounds any DLQ->function->DLQ loop.
	dlqSQS asyncSQSDeliverer
	dlqSNS asyncSNSPublisher
	mu     sync.Mutex // guards PublishVersion read-modify-write on funcData
	// inflightMu guards inflight, the number of invocations currently executing
	// per function name. It backs reserved-concurrency enforcement on Invoke.
	inflightMu sync.Mutex
	inflight   map[string]int
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetLogSink wires the CloudWatch Logs target that Invoke writes each
// invocation's START/END/REPORT lines (and any captured stdout/stderr) into,
// under the conventional /aws/lambda/<name> log group. Safe to leave unset —
// invocation-log surfacing is then skipped, so library users are unaffected.
func (m *Mock) SetLogSink(l logdriver.Logging) {
	m.logs = l
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
		inflight: make(map[string]int),
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

	if err := validateFunctionLimits(cfg.Memory, cfg.Timeout); err != nil {
		return nil, err
	}

	arn := idgen.AWSARN("lambda", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, "function:"+cfg.Name)
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
		// AWS always reports a TracingConfig, defaulting to PassThrough when the
		// client omits it.
		awsConfig: driver.AWSFunctionConfig{TracingConfig: &driver.TracingConfig{Mode: tracingModePassThrough}},
	})

	result := info

	return &result, nil
}

// validateFunctionLimits enforces the AWS MemorySize (128–10240 MB) and Timeout
// (1–900 s) create-time ranges, returning an InvalidArgument error the wire
// layer maps to InvalidParameterValueException / HTTP 400.
func validateFunctionLimits(memory, timeout int) error {
	if memory < minMemoryMB || memory > maxMemoryMB {
		return cerrors.Newf(cerrors.InvalidArgument,
			"'memorySize' value %d must be >= %d and <= %d", memory, minMemoryMB, maxMemoryMB)
	}

	if timeout < minTimeoutSecs || timeout > maxTimeoutSecs {
		return cerrors.Newf(cerrors.InvalidArgument,
			"'timeout' value %d must be >= %d and <= %d", timeout, minTimeoutSecs, maxTimeoutSecs)
	}

	return nil
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

	executedVersion, err := m.resolveQualifier(&fd, input.Qualifier, input.Payload)
	if err != nil {
		return nil, err
	}

	// A DryRun validates the function, qualifier, and permissions but runs
	// nothing and emits no metrics: resolveQualifier above has already rejected
	// an unknown alias/version with ResourceNotFoundException, so a DryRun that
	// reaches here is valid and reports 204 No Content.
	if input.InvokeType == invokeTypeDryRun {
		return &driver.InvokeOutput{StatusCode: dryRunStatusCode, ExecutedVersion: executedVersion}, nil
	}

	// Enforce reserved concurrency: an invoke that would push the in-flight count
	// past the function's reserved limit is throttled instead of executed.
	// release returns the slot once this invocation completes.
	release, err := m.reserveInvocationSlot(&fd, input.FunctionName)
	if err != nil {
		return nil, err
	}

	defer release()

	out, err := m.runInvocation(ctx, &fd, input, executedVersion)
	if err != nil {
		return nil, err
	}

	// An asynchronous (Event) invocation routes its finished outcome to the
	// function's async destinations: a failure (a handler/engine error) goes to
	// the DeadLetterConfig queue/topic and the OnFailure destination once retries
	// are exhausted; a success may go to the OnSuccess destination. Synchronous
	// invokes never touch these (the caller gets the result directly).
	if input.InvokeType == invokeTypeEvent {
		m.routeAsyncDestinations(ctx, &fd, input, executedVersion, out)
	}

	return out, nil
}

// runInvocation runs the function body and returns its InvokeOutput: the real
// FunctionEngine when one is deployed, the registered Go handler when one is
// attached, or the echo stub otherwise. It emits the invoke metrics and surfaces
// the START/END/REPORT logs; the async-destination routing sits above it in
// Invoke so both the stub/handler and engine paths funnel through one place.
func (m *Mock) runInvocation(
	ctx context.Context, fd *funcData, input driver.InvokeInput, executedVersion string,
) (*driver.InvokeOutput, error) {
	dims := map[string]string{"FunctionName": input.FunctionName}

	h := m.resolveHandler(fd, input.FunctionName)

	if h == nil && fd.engineBacked {
		return m.invokeEngine(ctx, input, dims, executedVersion)
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
		m.surfaceInvokeLogs(ctx, input.FunctionName, executedVersion, "", "")

		payload := input.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}

		return &driver.InvokeOutput{StatusCode: 200, Payload: payload, ExecutedVersion: executedVersion}, nil
	}

	payload, err := h(ctx, input.Payload)
	if err != nil {
		m.emitMetric(ctx, "Invocations", 1, dims)
		m.emitMetric(ctx, "Errors", 1, dims)
		m.surfaceInvokeLogs(ctx, input.FunctionName, executedVersion, "", err.Error())

		return &driver.InvokeOutput{StatusCode: 500, Error: err.Error(), ExecutedVersion: executedVersion}, nil
	}

	m.emitMetric(ctx, "Invocations", 1, dims)
	m.emitMetric(ctx, "Duration", 1.0, dims)
	m.emitMetric(ctx, "ConcurrentExecutions", 1, dims)
	m.surfaceInvokeLogs(ctx, input.FunctionName, executedVersion, "", "")

	return &driver.InvokeOutput{StatusCode: 200, Payload: payload, ExecutedVersion: executedVersion}, nil
}

// resolveHandler returns the Go handler to run for an invoke: the one attached
// to the function record, else one registered out-of-band via RegisterHandler,
// or nil when the function has no Go handler (a stub or engine-backed invoke).
func (m *Mock) resolveHandler(fd *funcData, name string) driver.HandlerFunc {
	if fd.handler != nil {
		return fd.handler
	}

	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock()

	return m.handlers[name]
}

// resolveQualifier resolves an Invoke Qualifier (empty, "$LATEST", a numeric
// published version, or an alias name) to the concrete version that should be
// reported as ExecutedVersion, matching real Lambda's alias-resolution
// semantics: an alias resolves one hop to a FunctionVersion (aliases can't
// point to other aliases); a version qualifier must name a version that was
// actually published. A weighted alias splits between its primary
// FunctionVersion and the versions in RoutingConfig.AdditionalVersionWeights;
// routingKey (the invoke payload) selects one deterministically (see
// selectAliasVersion). An unknown qualifier is a ResourceNotFoundException,
// the same error AWS returns for an alias or version that doesn't exist.
func (m *Mock) resolveQualifier(fd *funcData, qualifier string, routingKey []byte) (string, error) {
	if qualifier == "" || qualifier == latestVersion {
		return latestVersion, nil
	}

	if ad, ok := fd.aliases.Get(qualifier); ok {
		return selectAliasVersion(&ad.alias, routingKey), nil
	}

	if m.versionExists(fd, qualifier) {
		return qualifier, nil
	}

	return "", cerrors.Newf(cerrors.NotFound, "function version/alias not found for %s", qualifier)
}

// selectAliasVersion picks the concrete version a weighted alias routes an
// invocation to: the additional versions in RoutingConfig.AdditionalVersionWeights
// receive their configured fractions of traffic, the alias's primary
// FunctionVersion the remainder. The choice is deterministic in routingKey
// (the invoke payload) — a given event always routes to the same version, and
// across many distinct events the split approaches the configured weights — so
// tests can assert the distribution without flakiness. An alias with no weights
// always resolves to its primary FunctionVersion.
func selectAliasVersion(a *driver.Alias, routingKey []byte) string {
	rc := a.RoutingConfig
	if rc == nil || len(rc.AdditionalVersionWeights) == 0 {
		return a.FunctionVersion
	}

	// Iterate additional versions in a stable (sorted) order so the cumulative
	// bands are reproducible; the primary version owns [sum,1).
	versions := make([]string, 0, len(rc.AdditionalVersionWeights))
	for v := range rc.AdditionalVersionWeights {
		versions = append(versions, v)
	}

	sort.Strings(versions)

	point := hashToUnitFloat(routingKey)
	cumulative := 0.0

	for _, v := range versions {
		cumulative += rc.AdditionalVersionWeights[v]
		if point < cumulative {
			return v
		}
	}

	return a.FunctionVersion
}

// hashToUnitFloat maps arbitrary bytes to a point in [0.0,1.0) via a SHA-256
// digest, giving a stable, uniformly spread value for weighted-alias routing.
func hashToUnitFloat(key []byte) float64 {
	sum := sha256.Sum256(key)
	// Divide a 64-bit prefix by 2^64 to land in [0,1).
	return float64(binary.BigEndian.Uint64(sum[:8])) / (1 << 64)
}

// InvokeExternal asynchronously invokes the function identified by its ARN with
// the given event payload. It backs cross-service event delivery (e.g. S3 ->
// Lambda notifications, DynamoDB Streams / SQS event source mappings). An
// unknown function is a no-op so a stale target never fails the caller. A
// handler that runs but raises (StatusCode 500 / a non-empty FunctionError,
// exactly as Invoke reports it — see invoke's X-Amz-Function-Error semantics)
// is surfaced here as a genuine error, unlike Invoke itself: callers that only
// care whether delivery succeeded (S3, DynamoDB Streams) already discard
// InvokeExternal's error, while a caller that must react to handler failure
// (SQS: delete the message on success, leave it for redrive on failure) can.
//
// It is also the single choke point every such delivery path funnels through,
// so it carries the recursive-loop guard: a mapped/notified handler commonly
// writes back into its own event source (mark-processed, audit-append,
// status-bump), which re-enters here through the same synchronous call chain
// (write -> deliver -> Invoke -> handler -> write -> ...). Left unbounded that
// recurses the process into an unrecoverable "fatal error: stack overflow".
// ctx carries the re-entrant delivery depth (see internal/recursionguard);
// once it reaches recursionguard.MaxDepth — matching AWS Lambda's own
// recursive-loop detection, which stops invoking a function after ~16
// invocations within one chain of requests (see
// https://docs.aws.amazon.com/lambda/latest/dg/invocation-recursion.html) —
// further delivery is dropped instead of recursing.
func (m *Mock) InvokeExternal(ctx context.Context, functionARN string, payload []byte) error {
	name := functionNameFromARN(functionARN)
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

// functionNameFromARN extracts the function name from a Lambda ARN
// (arn:aws:lambda:region:account:function:name[:qualifier]); a bare name is
// returned unchanged.
func functionNameFromARN(arn string) string {
	const marker = ":function:"

	i := strings.Index(arn, marker)
	if i < 0 {
		return arn
	}

	name := arn[i+len(marker):]
	if j := strings.IndexByte(name, ':'); j >= 0 { // strip a version/alias qualifier
		name = name[:j]
	}

	return name
}

// invokeEngine runs a function whose code was deployed to the configured
// FunctionEngine and records the same metrics as a real handler invocation. A
// handler that raised is reported via out.Error (HTTP stays 200), matching real
// Lambda's X-Amz-Function-Error semantics.
func (m *Mock) invokeEngine(
	ctx context.Context, input driver.InvokeInput, dims map[string]string, executedVersion string,
) (*driver.InvokeOutput, error) {
	out, err := funcengine.Invoke(ctx, m.opts.FunctionEngine, input.FunctionName, input.Payload)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "invoke function %s: %v", input.FunctionName, err)
	}

	out.ExecutedVersion = executedVersion

	m.emitMetric(ctx, "Invocations", 1, dims)

	if out.Error != "" {
		m.emitMetric(ctx, "Errors", 1, dims)
		m.surfaceInvokeLogs(ctx, input.FunctionName, executedVersion, out.Logs, out.Error)

		return out, nil
	}

	m.emitMetric(ctx, "Duration", 1.0, dims)
	m.emitMetric(ctx, "ConcurrentExecutions", 1, dims)
	m.surfaceInvokeLogs(ctx, input.FunctionName, executedVersion, out.Logs, "")

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

func cloneVPCConfig(v *driver.VPCConfig) *driver.VPCConfig {
	if v == nil {
		return nil
	}

	out := &driver.VPCConfig{VpcID: v.VpcID}
	out.SubnetIDs = append([]string(nil), v.SubnetIDs...)
	out.SecurityGroupIDs = append([]string(nil), v.SecurityGroupIDs...)

	return out
}

func cloneDeadLetterConfig(d *driver.DeadLetterConfig) *driver.DeadLetterConfig {
	if d == nil {
		return nil
	}

	out := *d

	return &out
}

func cloneTracingConfig(t *driver.TracingConfig) *driver.TracingConfig {
	if t == nil {
		return nil
	}

	out := *t

	return &out
}

func cloneArchitectures(a []string) []string {
	if a == nil {
		return nil
	}

	return append([]string(nil), a...)
}

func cloneEphemeralStorage(e *driver.EphemeralStorage) *driver.EphemeralStorage {
	if e == nil {
		return nil
	}

	out := *e

	return &out
}

func cloneLayers(l []driver.FunctionLayer) []driver.FunctionLayer {
	if l == nil {
		return nil
	}

	return append([]driver.FunctionLayer(nil), l...)
}

// tracingConfigOrDefault returns a copy of t, or the AWS default
// {Mode: "PassThrough"} when the client supplied no tracing configuration.
func tracingConfigOrDefault(t *driver.TracingConfig) *driver.TracingConfig {
	if t == nil || t.Mode == "" {
		return &driver.TracingConfig{Mode: tracingModePassThrough}
	}

	return cloneTracingConfig(t)
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
