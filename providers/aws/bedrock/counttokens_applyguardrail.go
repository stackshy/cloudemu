package bedrock

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// CountTokens estimates the input token count for a would-be inference request.
// When a raw InvokeModel body is supplied it counts the extracted prompt;
// otherwise it counts the Converse messages plus any system content.
//
//nolint:gocritic // in matches the driver interface signature; read without mutation.
func (m *Mock) CountTokens(_ context.Context, in driver.CountTokensInput) (int, error) {
	if in.ModelID == "" {
		return 0, errors.New(errors.InvalidArgument, "modelId is required")
	}

	if !m.modelExists(in.ModelID) {
		return 0, errors.Newf(errors.InvalidArgument, "model %q not found", in.ModelID)
	}

	if len(in.InvokeBody) > 0 {
		return wordCount(extractPrompt(in.InvokeBody)), nil
	}

	return conversationTokens(in.Messages) + wordCount(strings.Join(in.System, " ")), nil
}

// ApplyGuardrail evaluates content against a guardrail. The emulator never
// intervenes: it validates the request and echoes the input content back
// through as outputs with a NONE action.
func (m *Mock) ApplyGuardrail(_ context.Context, in driver.ApplyGuardrailInput) (*driver.ApplyGuardrailOutput, error) {
	if in.GuardrailIdentifier == "" {
		return nil, errors.New(errors.InvalidArgument, "guardrailIdentifier is required")
	}

	rec := m.findGuardrailRecord(in.GuardrailIdentifier)
	if rec == nil {
		return nil, errors.Newf(errors.NotFound, "guardrail %q not found", in.GuardrailIdentifier)
	}

	// A specific numbered version must exist; "" and "DRAFT" resolve to the
	// working draft, which always exists.
	if v := in.GuardrailVersion; v != "" && v != guardrailDraftVersion {
		if _, ok := rec.versionSnapshot(v); !ok {
			return nil, errors.Newf(errors.NotFound, "guardrail %q version %q not found", in.GuardrailIdentifier, v)
		}
	}

	if in.Source != driver.GuardrailSourceInput && in.Source != driver.GuardrailSourceOutput {
		return nil, errors.Newf(errors.InvalidArgument, "invalid source %q: want INPUT or OUTPUT", in.Source)
	}

	return &driver.ApplyGuardrailOutput{
		Action:  driver.GuardrailActionNone,
		Outputs: append([]string(nil), in.Content...),
	}, nil
}
