// Package sfn provides an in-memory mock implementation of AWS Step Functions.
//
// The mock stores each state machine's ASL definition verbatim and interprets
// it with a real state-graph walker (see providers/aws/sfn/asl): StartExecution
// walks from StartAt following Next/End, computes the terminal status/output,
// and records the per-state event list GetExecutionHistory returns. Execution is
// synchronous; the settle overlay keeps RUNNING observable under AsyncSettle.
// ctx is threaded end-to-end so the Task->Lambda seam carries recursionguard
// depth. Pass, Choice, Wait, Succeed, Fail and Task (Lambda, with Retry/Catch)
// are supported; Parallel and Map parse but fail loudly at run time until their
// handlers land.
package sfn

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// Compile-time check that Mock implements driver.SFN.
var _ driver.SFN = (*Mock)(nil)

// LambdaSyncInvoker is the Task->Lambda seam a Task state uses to invoke a
// function synchronously. It is deliberately named apart from the
// InvokeExternal-shaped LambdaInvoker used by eventbridge/s3/sns: those are
// fire-and-forget, whereas this returns the function output and a functionError
// (mapped to States.TaskFailed). The lambda backend's adapter carries the
// recursionguard depth so a Task->Lambda->StartExecution->Task cycle terminates
// at recursionguard.MaxDepth instead of overflowing the stack.
type LambdaSyncInvoker interface {
	InvokeSync(ctx context.Context, functionARN string, payload []byte) (output []byte, functionError string, err error)
}

const (
	// maxTags is the SFN cap on tags per resource.
	maxTags = 50
	// arnParts is the field count of a fully-qualified AWS ARN.
	arnParts = 6
	// arnScheme is the leading segment of every ARN.
	arnScheme = "arn"
	// statesService is the SFN service segment of a states ARN.
	statesService = "states"
	// iamService is the IAM service segment of a role ARN.
	iamService = "iam"
	// emptyJSON is the default output/input payload when none is supplied.
	emptyJSON = "{}"
)

// Mock is an in-memory implementation of AWS Step Functions.
type Mock struct {
	machines   *memstore.Store[*smData]
	executions *memstore.Store[*execData]
	activities *memstore.Store[*actData]
	aliases    *memstore.Store[*aliasData]
	mapRuns    *memstore.Store[*mapRunData]

	tasksMu sync.RWMutex
	tasks   map[string]string // taskToken -> executionArn (activity task bookkeeping)

	// lambdaSync is the Task->Lambda seam; nil until SetLambdaSyncInvoker wires
	// the Lambda backend (library-only construction leaves Task echoing input).
	lambdaSync LambdaSyncInvoker

	opts *config.Options
}

// SetLambdaSyncInvoker wires the Lambda backend so a Task state
// (arn:aws:states:::lambda:invoke or a bare Lambda function ARN) invokes the
// function synchronously through the recursion-guarded seam.
func (m *Mock) SetLambdaSyncInvoker(i LambdaSyncInvoker) {
	m.lambdaSync = i
}

// smData is a state machine plus its own lock.
type smData struct {
	sm driver.StateMachine
	mu sync.RWMutex
}

// execData is an execution plus its own lock.
type execData struct {
	exec driver.Execution
	// settle overlays a RUNNING window over the stored (terminal) status on the
	// Describe surface under AsyncSettle; zero-value reports the stored status
	// immediately. A running execution has no stop date or output yet.
	settle settle.Window
	mu     sync.RWMutex
}

// actData is an activity plus its own lock.
type actData struct {
	act driver.Activity
	mu  sync.RWMutex
}

// aliasData is a state machine alias plus its own lock.
type aliasData struct {
	alias driver.Alias
	mu    sync.RWMutex
}

// mapRunData is a Map Run plus its own lock.
type mapRunData struct {
	run driver.MapRun
	mu  sync.RWMutex
}

// New creates a new Step Functions mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		machines:   memstore.New[*smData](),
		executions: memstore.New[*execData](),
		activities: memstore.New[*actData](),
		aliases:    memstore.New[*aliasData](),
		mapRuns:    memstore.New[*mapRunData](),
		tasks:      make(map[string]string),
		opts:       opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) smARN(region, name string) string {
	return idgen.AWSARN("states", region, m.opts.AccountID, "stateMachine:"+name)
}

func (m *Mock) execARN(region, smName, execName string) string {
	return idgen.AWSARN("states", region, m.opts.AccountID, "execution:"+smName+":"+execName)
}

func (m *Mock) activityARN(region, name string) string {
	return idgen.AWSARN("states", region, m.opts.AccountID, "activity:"+name)
}

func (m *Mock) aliasARN(region, smName, alias string) string {
	return idgen.AWSARN("states", region, m.opts.AccountID, "stateMachine:"+smName+":"+alias)
}

func (m *Mock) mapRunARN(region, smName, execName, id string) string {
	return idgen.AWSARN("states", region, m.opts.AccountID, "mapRun:"+smName+"/"+execName+":"+id)
}

// arnRegion returns the region field of a Step Functions ARN
// (arn:aws:states:<region>:<account>:<resource>), or fallback when the ARN is
// malformed. A parent resource's stored ARN is the source of truth for the
// region of the child ARNs derived from it (executions, aliases, map runs), so
// a child always shares its parent's region.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
}

// smNameFromARN extracts the state machine name from a state machine ARN.
func smNameFromARN(arn string) string {
	seg := strings.SplitN(arn, ":", arnParts)
	if len(seg) != arnParts {
		return ""
	}

	return strings.TrimPrefix(seg[5], "stateMachine:")
}

// statesResourcePrefix reports whether arn is a well-formed states-service ARN
// whose resource segment carries the given prefix (e.g. "stateMachine:").
func statesResourcePrefix(arn, prefix string) bool {
	seg := strings.SplitN(arn, ":", arnParts)
	if len(seg) != arnParts {
		return false
	}

	return seg[0] == arnScheme && seg[2] == statesService && strings.HasPrefix(seg[5], prefix)
}

// validStateMachineARN reports whether arn has the SFN state machine ARN shape.
func validStateMachineARN(arn string) bool {
	return statesResourcePrefix(arn, "stateMachine:") && smNameFromARN(arn) != ""
}

// validExecutionARN reports whether arn has the SFN execution ARN shape.
func validExecutionARN(arn string) bool {
	return statesResourcePrefix(arn, "execution:")
}

// validActivityARN reports whether arn has the SFN activity ARN shape.
func validActivityARN(arn string) bool {
	return statesResourcePrefix(arn, "activity:")
}

// validMapRunARN reports whether arn has the SFN Map Run ARN shape.
func validMapRunARN(arn string) bool {
	return statesResourcePrefix(arn, "mapRun:")
}

// validRoleARN reports whether arn has the IAM role ARN shape
// (arn:<partition>:iam::<account>:role/<name>). Step Functions requires a
// valid IAM role ARN on CreateStateMachine and rejects anything else as
// InvalidArn.
func validRoleARN(arn string) bool {
	seg := strings.SplitN(arn, ":", arnParts)
	if len(seg) != arnParts {
		return false
	}

	return seg[0] == arnScheme && seg[2] == iamService &&
		strings.HasPrefix(seg[5], "role/") && len(seg[5]) > len("role/")
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
