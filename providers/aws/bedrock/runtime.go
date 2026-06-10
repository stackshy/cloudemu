package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/bedrock/driver"
	"github.com/stackshy/cloudemu/errors"
)

const (
	contentTypeJSON = "application/json"
	stopReasonTurn  = "end_turn"
	defaultLatency  = 42 // deterministic emulated latency in milliseconds

	roleUser = "user"

	familyAnthropic = "anthropic"
	familyTitan     = "titan"
	familyLlama     = "llama"
	familyCohere    = "cohere"
	familyGeneric   = "generic"
)

// InvokeModel runs emulated inference, returning a response envelope shaped to
// match the requested model's family so real SDK callers can parse it.
func (m *Mock) InvokeModel(_ context.Context, in driver.InvokeModelInput) (*driver.InvokeModelResult, error) {
	if in.ModelID == "" {
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	}

	if !m.modelExists(in.ModelID) {
		return nil, errors.Newf(errors.InvalidArgument, "model %q not found", in.ModelID)
	}

	prompt := extractPrompt(in.Body)
	text := completion(in.ModelID, prompt)

	body, err := encodeInvokeResponse(in.ModelID, text, wordCount(prompt), wordCount(text))
	if err != nil {
		return nil, errors.Newf(errors.Internal, "encode response: %v", err)
	}

	return &driver.InvokeModelResult{ContentType: contentTypeJSON, Body: body}, nil
}

// Converse runs emulated inference for the structured Converse API.
func (m *Mock) Converse(_ context.Context, in driver.ConverseInput) (*driver.ConverseOutput, error) {
	if in.ModelID == "" {
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	}

	if !m.modelExists(in.ModelID) {
		return nil, errors.Newf(errors.InvalidArgument, "model %q not found", in.ModelID)
	}

	if len(in.Messages) == 0 {
		return nil, errors.New(errors.InvalidArgument, "at least one message is required")
	}

	prompt := lastUserText(in.Messages)
	text := completion(in.ModelID, prompt)

	inTokens := wordCount(strings.Join(in.System, " ")) + conversationTokens(in.Messages)
	outTokens := wordCount(text)

	return &driver.ConverseOutput{
		Message:      driver.Message{Role: "assistant", Text: []string{text}},
		StopReason:   stopReasonTurn,
		InputTokens:  inTokens,
		OutputTokens: outTokens,
		TotalTokens:  inTokens + outTokens,
		LatencyMs:    defaultLatency,
	}, nil
}

// completion produces deterministic response text for a model and prompt.
func completion(modelID, prompt string) string {
	if prompt == "" {
		return fmt.Sprintf("This is a simulated response from %s.", modelID)
	}

	return fmt.Sprintf("This is a simulated response from %s to: %s", modelID, prompt)
}

// encodeInvokeResponse marshals text into the response envelope of modelID's
// family.
func encodeInvokeResponse(modelID, text string, inTokens, outTokens int) ([]byte, error) {
	switch familyOf(modelID) {
	case familyAnthropic:
		return json.Marshal(anthropicResponse{
			ID: "msg_" + "sim", Type: "message", Role: "assistant", Model: modelID,
			Content:    []anthropicContent{{Type: "text", Text: text}},
			StopReason: stopReasonTurn,
			Usage:      anthropicUsage{InputTokens: inTokens, OutputTokens: outTokens},
		})
	case familyTitan:
		return json.Marshal(titanResponse{
			InputTextTokenCount: inTokens,
			Results:             []titanResult{{TokenCount: outTokens, OutputText: text, CompletionReason: "FINISH"}},
		})
	case familyLlama:
		return json.Marshal(llamaResponse{
			Generation: text, PromptTokenCount: inTokens, GenerationTokenCount: outTokens, StopReason: "stop",
		})
	case familyCohere:
		return json.Marshal(cohereResponse{Generations: []cohereGeneration{{Text: text, FinishReason: "COMPLETE"}}})
	default:
		return json.Marshal(genericResponse{Completion: text, StopReason: "stop"})
	}
}

// extractPrompt pulls a best-effort prompt string out of a model-native
// request body. Unrecognized shapes yield an empty prompt.
func extractPrompt(body []byte) string {
	var probe struct {
		Prompt    string `json:"prompt"`
		InputText string `json:"inputText"`
		Messages  []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}

	if json.Unmarshal(body, &probe) != nil {
		return ""
	}

	switch {
	case probe.Prompt != "":
		return probe.Prompt
	case probe.InputText != "":
		return probe.InputText
	}

	for i := len(probe.Messages) - 1; i >= 0; i-- {
		if probe.Messages[i].Role != roleUser {
			continue
		}

		if t := contentText(probe.Messages[i].Content); t != "" {
			return t
		}
	}

	return ""
}

// contentText reads a message content field that may be a plain string or an
// array of {type,text} blocks.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	var blocks []struct {
		Text string `json:"text"`
	}

	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}

	parts := make([]string, 0, len(blocks))

	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}

	return strings.Join(parts, " ")
}

// lastUserText returns the text of the last user-role message.
func lastUserText(msgs []driver.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == roleUser && len(msgs[i].Text) > 0 {
			return strings.Join(msgs[i].Text, " ")
		}
	}

	return ""
}

// conversationTokens estimates input tokens across all message text.
func conversationTokens(msgs []driver.Message) int {
	total := 0
	for _, msg := range msgs {
		total += wordCount(strings.Join(msg.Text, " "))
	}

	return total
}

// wordCount is a crude token estimate: whitespace-separated words.
func wordCount(s string) int {
	return len(strings.Fields(s))
}
