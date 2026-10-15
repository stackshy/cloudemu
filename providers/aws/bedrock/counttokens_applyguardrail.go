package bedrock

import (
	"context"
	"strings"
	"unicode"

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

	if m.resolveModel(in.ModelID) == nil {
		return 0, errors.New(errors.InvalidArgument, msgInvalidModelID)
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
	g := rec.draftSnapshot()

	if v := in.GuardrailVersion; v != "" && v != guardrailDraftVersion {
		snap, ok := rec.versionSnapshot(v)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "guardrail %q version %q not found", in.GuardrailIdentifier, v)
		}

		g = snap
	}

	if in.Source != driver.GuardrailSourceInput && in.Source != driver.GuardrailSourceOutput {
		return nil, errors.Newf(errors.InvalidArgument, "invalid source %q: want INPUT or OUTPUT", in.Source)
	}

	if deniedWordIn(g.WordPolicy, in.Content) {
		msg := g.BlockedInputMessaging
		if in.Source == driver.GuardrailSourceOutput {
			msg = g.BlockedOutputsMessaging
		}

		return &driver.ApplyGuardrailOutput{Action: driver.GuardrailActionIntervened, Outputs: []string{msg}}, nil
	}

	return &driver.ApplyGuardrailOutput{
		Action:  driver.GuardrailActionNone,
		Outputs: append([]string(nil), in.Content...),
	}, nil
}

// deniedWordIn reports whether any content contains one of the word policy's
// custom denied words (case-insensitive, whole words or phrases). Managed word
// lists (PROFANITY) are not evaluated.
func deniedWordIn(policy *driver.GuardrailWordPolicy, content []string) bool {
	if policy == nil {
		return false
	}

	for _, c := range content {
		text := " " + strings.ToLower(strings.Join(strings.FieldsFunc(c, notWordRune), " ")) + " "

		for _, w := range policy.Words {
			word := strings.Join(strings.FieldsFunc(strings.ToLower(w.Text), notWordRune), " ")
			if word != "" && strings.Contains(text, " "+word+" ") {
				return true
			}
		}
	}

	return false
}

func notWordRune(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
