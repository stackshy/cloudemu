package bedrockagentruntime

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// OptimizePrompt limits from the service model.
const (
	maxPromptLength  = 200000
	maxModelIDLength = 2048
	modelIDPattern   = `^(arn:aws(-[^:]+)?:bedrock:[a-z0-9-]{1,20}:(([0-9]{12}:custom-model/[a-z0-9-]{1,63}[.]{1}` +
		`[a-z0-9-]{1,63}/[a-z0-9]{12})|(:foundation-model/[a-z0-9-]{1,63}[.]{1}[a-z0-9-]{1,63}([.:]?[a-z0-9-]{1,63}))|` +
		`([0-9]{12}:provisioned-model/[a-z0-9]{12})))|([a-z0-9-]{1,63}[.]{1}[a-z0-9-]{1,63}([.:]?[a-z0-9-]{1,63}))|` +
		`(([0-9a-zA-Z][_-]?)+)$`
)

var modelIDRE = regexp.MustCompile(modelIDPattern)

// OptimizePrompt analyzes a prompt and rewrites it for the target model. With
// no model to rewrite with, the rewrite is deterministic: the prompt's
// whitespace normalized under a short task framing for the model.
func (*Mock) OptimizePrompt(_ context.Context, text, targetModelID string) (*driver.OptimizedPrompt, error) {
	var v violations

	v.required("input.textPrompt.text", text == "")
	v.maxLen("input.textPrompt.text", text, maxPromptLength)
	v.required("targetModelId", targetModelID == "")
	v.maxLen("targetModelId", targetModelID, maxModelIDLength)
	v.pattern("targetModelId", targetModelID, modelIDRE, modelIDPattern)

	if err := v.err(); err != nil {
		return nil, err
	}

	clean := strings.Join(strings.Fields(text), " ")

	return &driver.OptimizedPrompt{
		Analysis: fmt.Sprintf("The prompt has %d words. It was restructured for %s with a clear task statement.",
			len(strings.Fields(text)), targetModelID),
		Text: "You are an assistant. Complete the following task precisely and concisely.\n\nTask: " + clean,
	}, nil
}
