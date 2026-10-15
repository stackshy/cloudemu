package bedrockagentruntime

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Guardrail actions as the runtime reports them (GuadrailAction).
const (
	guardrailInput      = bedrockdriver.GuardrailSourceInput
	guardrailOutput     = bedrockdriver.GuardrailSourceOutput
	guardrailNone       = "NONE"
	guardrailIntervened = "INTERVENED"
)

// applyGuardrail evaluates text against a Bedrock guardrail. It returns
// INTERVENED and the guardrail's blocked message when the guardrail blocks the
// text, NONE otherwise; with no guardrail (or no guardrail service) it returns
// "". An unknown guardrail is a ResourceNotFoundException.
func (m *Mock) applyGuardrail(ctx context.Context, g *driver.GuardrailRef, source, text string) (action, msg string, err error) {
	if g == nil || m.guardrails == nil {
		return "", "", nil
	}

	out, err := m.guardrails.ApplyGuardrail(ctx, bedrockdriver.ApplyGuardrailInput{
		GuardrailIdentifier: g.ID, GuardrailVersion: g.Version, Source: source, Content: []string{text},
	})
	if err != nil {
		if errors.IsNotFound(err) {
			return "", "", err
		}

		return "", "", errors.Newf(errors.InvalidArgument, "guardrail %s: %s", g.ID, errors.Message(err))
	}

	if out.Action == bedrockdriver.GuardrailActionIntervened {
		return guardrailIntervened, strings.Join(out.Outputs, " "), nil
	}

	return guardrailNone, "", nil
}
