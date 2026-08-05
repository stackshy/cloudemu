package azureai

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

// Compile-time check that Mock implements the data-plane surface.
var _ driver.DataPlane = (*Mock)(nil)

// embeddingDims is the fixed synthetic embedding width.
const embeddingDims = 16

// roleUser is the default/user chat role.
const roleUser = "user"

// FNV-1a parameters and the modulus used to derive deterministic embeddings.
const (
	fnvOffset uint32 = 2166136261
	fnvPrime  uint32 = 16777619
	embedMod  uint32 = 1000
)

func (m *Mock) nextID(prefix string) string {
	return prefix + "_" + idHex(m.seq.Add(1))
}

func idHex(n int64) string {
	const digits = "0123456789abcdef"

	if n == 0 {
		return "0"
	}

	var b [16]byte

	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n&0xf]
		n >>= 4
	}

	return string(b[i:])
}

func (m *Mock) unixNow() int64 {
	return m.opts.Clock.Now().Unix()
}

// approxTokens is a deterministic whitespace-based token estimate.
func approxTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}

	return len(strings.Fields(text))
}

// --- Azure OpenAI inference ---

func (m *Mock) ChatCompletions(
	_ context.Context, _, deployment string, req driver.ChatCompletionRequest,
) (*driver.ChatCompletionResponse, error) {
	prompt := 0
	last := ""

	for _, msg := range req.Messages {
		prompt += approxTokens(msg.Content)

		if msg.Role == roleUser {
			last = msg.Content
		}
	}

	reply := "Echo: " + last
	completion := approxTokens(reply)

	m.emitMetric("inference/chatCompletions", 1, map[string]string{"deployment": deployment})

	return &driver.ChatCompletionResponse{
		ID:      m.nextID("chatcmpl"),
		Model:   deployment,
		Created: m.unixNow(),
		Choices: []driver.ChatChoice{{
			Index:        0,
			Message:      driver.ChatMessage{Role: "assistant", Content: reply},
			FinishReason: "stop",
		}},
		Usage: driver.TokenUsage{
			PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
		},
	}, nil
}

func (m *Mock) Embeddings(
	_ context.Context, _, deployment string, req driver.EmbeddingsRequest,
) (*driver.EmbeddingsResponse, error) {
	data := make([]driver.EmbeddingData, 0, len(req.Input))
	prompt := 0

	for i, in := range req.Input {
		prompt += approxTokens(in)
		data = append(data, driver.EmbeddingData{Index: i, Embedding: embed(in)})
	}

	m.emitMetric("inference/embeddings", float64(len(req.Input)), map[string]string{"deployment": deployment})

	return &driver.EmbeddingsResponse{
		Model: deployment,
		Data:  data,
		Usage: driver.TokenUsage{PromptTokens: prompt, TotalTokens: prompt},
	}, nil
}

// embed returns a deterministic unit-ish vector derived from the input.
func embed(s string) []float64 {
	out := make([]float64, embeddingDims)
	h := fnvOffset

	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime
		out[i%embeddingDims] += float64(h%embedMod) / float64(embedMod)
	}

	return out
}

func (m *Mock) Completions(
	_ context.Context, _, deployment string, req driver.CompletionsRequest,
) (*driver.CompletionsResponse, error) {
	prompt := approxTokens(req.Prompt)
	text := "Echo: " + req.Prompt
	completion := approxTokens(text)

	m.emitMetric("inference/completions", 1, map[string]string{"deployment": deployment})

	return &driver.CompletionsResponse{
		ID:      m.nextID("cmpl"),
		Model:   deployment,
		Created: m.unixNow(),
		Choices: []driver.CompletionChoice{{Text: text, Index: 0, FinishReason: "stop"}},
		Usage:   driver.TokenUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion},
	}, nil
}

// --- Assistants API ---

func (m *Mock) CreateAssistant(_ context.Context, cfg driver.AssistantConfig) (*driver.Assistant, error) {
	if cfg.Model == "" {
		return nil, errors.New(errors.InvalidArgument, "model is required")
	}

	a := &driver.Assistant{
		ID: m.nextID("asst"), Model: cfg.Model, Name: cfg.Name,
		Instructions: cfg.Instructions, CreatedAt: m.unixNow(),
	}
	m.assistants.Set(key(cfg.Account, a.ID), a)

	out := *a

	return &out, nil
}

func (m *Mock) GetAssistant(_ context.Context, account, id string) (*driver.Assistant, error) {
	a, ok := m.assistants.Get(key(account, id))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "assistant %q not found", id)
	}

	out := *a

	return &out, nil
}

func (m *Mock) ListAssistants(_ context.Context, account string) ([]driver.Assistant, error) {
	prefix := account + "/"
	out := make([]driver.Assistant, 0)

	for k, a := range m.assistants.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *a)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return seqFromID(out[i].ID) < seqFromID(out[j].ID) })

	return out, nil
}

func (m *Mock) DeleteAssistant(_ context.Context, account, id string) error {
	if !m.assistants.Delete(key(account, id)) {
		return errors.Newf(errors.NotFound, "assistant %q not found", id)
	}

	return nil
}

func (m *Mock) CreateThread(_ context.Context, account string) (*driver.Thread, error) {
	t := &driver.Thread{ID: m.nextID("thread"), CreatedAt: m.unixNow()}
	m.threads.Set(key(account, t.ID), t)

	out := *t

	return &out, nil
}

func (m *Mock) GetThread(_ context.Context, account, id string) (*driver.Thread, error) {
	t, ok := m.threads.Get(key(account, id))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "thread %q not found", id)
	}

	out := *t

	return &out, nil
}

func (m *Mock) DeleteThread(_ context.Context, account, id string) error {
	if !m.threads.Delete(key(account, id)) {
		return errors.Newf(errors.NotFound, "thread %q not found", id)
	}

	return nil
}

func (m *Mock) CreateMessage(_ context.Context, account, thread, role, content string) (*driver.ThreadMessage, error) {
	if !m.threads.Has(key(account, thread)) {
		return nil, errors.Newf(errors.NotFound, "thread %q not found", thread)
	}

	if role == "" {
		role = roleUser
	}

	msg := &driver.ThreadMessage{
		ID: m.nextID("msg"), ThreadID: thread, Role: role, Content: content, CreatedAt: m.unixNow(),
	}
	m.messages.Set(key(account, thread, msg.ID), msg)

	out := *msg

	return &out, nil
}

func (m *Mock) ListMessages(_ context.Context, account, thread string) ([]driver.ThreadMessage, error) {
	if !m.threads.Has(key(account, thread)) {
		return nil, errors.Newf(errors.NotFound, "thread %q not found", thread)
	}

	prefix := key(account, thread) + "/"
	out := make([]driver.ThreadMessage, 0)

	for k, msg := range m.messages.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *msg)
		}
	}

	// Return in creation order; message IDs carry a monotonic sequence.
	sort.SliceStable(out, func(i, j int) bool { return seqFromID(out[i].ID) < seqFromID(out[j].ID) })

	return out, nil
}

// seqFromID extracts the monotonic sequence encoded as the hex suffix of an ID
// minted by nextID ("<prefix>_<hex>"). Unparseable IDs sort first.
func seqFromID(id string) int64 {
	_, hexPart, ok := strings.Cut(id, "_")
	if !ok {
		return -1
	}

	n, err := strconv.ParseInt(hexPart, 16, 64)
	if err != nil {
		return -1
	}

	return n
}

func (m *Mock) CreateRun(_ context.Context, account, thread, assistant string) (*driver.Run, error) {
	if !m.threads.Has(key(account, thread)) {
		return nil, errors.Newf(errors.NotFound, "thread %q not found", thread)
	}

	if !m.assistants.Has(key(account, assistant)) {
		return nil, errors.Newf(errors.NotFound, "assistant %q not found", assistant)
	}

	// Runs complete synchronously in the emulator.
	run := &driver.Run{
		ID: m.nextID("run"), ThreadID: thread, AssistantID: assistant,
		Status: "completed", CreatedAt: m.unixNow(),
	}
	m.runs.Set(key(account, thread, run.ID), run)

	out := *run

	return &out, nil
}

func (m *Mock) GetRun(_ context.Context, account, thread, id string) (*driver.Run, error) {
	run, ok := m.runs.Get(key(account, thread, id))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "run %q not found", id)
	}

	out := *run

	return &out, nil
}

// --- AML online-endpoint scoring (filled by the AML phase's data plane) ---

func (m *Mock) ScoreOnlineEndpoint(_ context.Context, endpoint string, body []byte) ([]byte, error) {
	if endpoint == "" {
		return nil, errors.New(errors.InvalidArgument, "endpoint is required")
	}

	m.emitMetric("inference/score", 1, map[string]string{"endpoint": endpoint})

	out := make([]byte, len(body))
	copy(out, body)

	return out, nil
}
