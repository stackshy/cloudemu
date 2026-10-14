// Package bedrockagentruntime provides an in-memory mock implementation of the
// AWS Bedrock Agent runtime (bedrock-agent-runtime): agent and inline-agent
// invocation, knowledge-base retrieval and retrieve-and-generate, flow
// invocation and executions, sessions, agent memory, rerank, query generation
// and prompt optimization.
//
// Wired to the bedrock-agent control plane (SetControlPlane) it resolves real
// agents, aliases, versions, action groups, knowledge bases, documents and
// flows, runs action groups (return control or the Lambda mock), applies
// Bedrock guardrails and publishes AWS/Bedrock/Agents metrics. Used alone it
// keeps answering with deterministic simulated text and validates nothing
// beyond required members, as it always did.
package bedrockagentruntime

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time checks that Mock implements driver.BedrockAgentRuntime and
// every optional capability.
var (
	_ driver.BedrockAgentRuntime          = (*Mock)(nil)
	_ driver.Sessions                     = (*Mock)(nil)
	_ driver.Invocations                  = (*Mock)(nil)
	_ driver.InlineAgents                 = (*Mock)(nil)
	_ driver.FlowExecutions               = (*Mock)(nil)
	_ driver.Reranker                     = (*Mock)(nil)
	_ driver.AgentMemory                  = (*Mock)(nil)
	_ driver.Tagger                       = (*Mock)(nil)
	_ driver.QueryGenerator               = (*Mock)(nil)
	_ driver.PromptOptimizer              = (*Mock)(nil)
	_ driver.AgenticRetriever             = (*Mock)(nil)
	_ driver.DocumentReader               = (*Mock)(nil)
	_ driver.StreamingRetrieveAndGenerate = (*Mock)(nil)
)

const contentTypeJSON = "application/json"

// Mock is an in-memory mock implementation of the AWS Bedrock Agent runtime.
type Mock struct {
	opts *config.Options

	// Collaborators wired by the provider factory; each is optional.
	cp         ControlPlane
	models     Models
	guardrails Guardrails
	lambda     Lambda
	monitoring mondriver.Monitoring
	objects    ObjectReader

	sessions      *memstore.Store[*driver.Session]
	sessionTags   *memstore.Store[map[string]string]
	invocations   *memstore.Store[*driver.Invocation]     // "<session>/<invocation>"
	steps         *memstore.Store[*driver.InvocationStep] // "<session>/<invocation>/<step>"
	conversations *memstore.Store[*conversation]          // "<agent>/<alias>/<session>"
	memories      *memstore.Store[*driver.MemorySummary]  // "<agent>/<alias>/<memory>/<session>"
	executions    *memstore.Store[*execution]             // execution ARN
	docTokens     *memstore.Store[*docToken]
	execSettle    *settle.Set

	// mu serializes compound writes: session state transitions with the
	// invocation and step inserts that depend on them, and execution stop
	// versus settle.
	mu sync.Mutex

	// turnLocks holds one mutex per conversation key: the turns of one
	// conversation run one at a time, without holding mu, so a turn's calls
	// out (model, Lambda, guardrail) never block other sessions and a Lambda
	// action group can call back into the runtime.
	turnLocks sync.Map
}

// New creates a new Bedrock Agent runtime mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		opts:          opts,
		sessions:      memstore.New[*driver.Session](),
		sessionTags:   memstore.New[map[string]string](),
		invocations:   memstore.New[*driver.Invocation](),
		steps:         memstore.New[*driver.InvocationStep](),
		conversations: memstore.New[*conversation](),
		memories:      memstore.New[*driver.MemorySummary](),
		executions:    memstore.New[*execution](),
		docTokens:     memstore.New[*docToken](),
		execSettle:    settle.NewSet(),
	}
}

// SetControlPlane links the runtime to the bedrock-agent control plane, whose
// agents, aliases, knowledge bases and flows it then resolves and validates.
func (m *Mock) SetControlPlane(cp ControlPlane) { m.cp = cp }

// SetModels sets the Bedrock model runtime that generates agent answers.
func (m *Mock) SetModels(models Models) { m.models = models }

// SetGuardrails sets the Bedrock guardrails applied to inputs and outputs.
func (m *Mock) SetGuardrails(g Guardrails) { m.guardrails = g }

// SetLambda sets the Lambda service that runs Lambda-backed action groups.
func (m *Mock) SetLambda(l Lambda) { m.lambda = l }

// SetMonitoring sets the CloudWatch backend for AWS/Bedrock/Agents metrics.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) { m.monitoring = mon }

// SetObjectStore sets the S3 backend RetrieveAndGenerate reads S3 external
// sources from.
func (m *Mock) SetObjectStore(r ObjectReader) { m.objects = r }

func (m *Mock) now() time.Time { return m.opts.Clock.Now().UTC() }

func (m *Mock) nowString() string { return m.now().Format(time.RFC3339Nano) }

// newUUID returns a random RFC 4122 version-4 UUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	b[6] = (b[6] & 0x0f) | 0x40 // UUID version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// sortByTimeDesc orders items newest first by an RFC 3339 key.
func sortByTimeDesc[T any](items []T, key func(T) string) {
	slices.SortStableFunc(items, func(a, b T) int { return strings.Compare(key(b), key(a)) })
}

// copyRaw returns a copy of b that shares no memory with it.
func copyRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}

	return append(json.RawMessage(nil), b...)
}

// isNull reports whether a raw JSON member is absent or an explicit null.
func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)

	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}
