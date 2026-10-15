package bedrockagentruntime

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type optimizePromptRequest struct {
	Input struct {
		TextPrompt struct {
			Text string `json:"text"`
		} `json:"textPrompt"`
	} `json:"input"`
	TargetModelID string `json:"targetModelId"`
}

// serveOptimizePrompt handles OptimizePrompt: an analyzePromptEvent, then an
// optimizedPromptEvent.
func serveOptimizePrompt(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	withCapability(h, w, "OptimizePrompt", func(c driver.PromptOptimizer) {
		var in optimizePromptRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		res, err := c.OptimizePrompt(r.Context(), in.Input.TextPrompt.Text, in.TargetModelID)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeEventStream(w, nil, []streamEvent{
			{"analyzePromptEvent", map[string]string{"message": res.Analysis}},
			{"optimizedPromptEvent", map[string]any{"optimizedPrompt": map[string]any{"textPrompt": map[string]string{"text": res.Text}}}},
		})
	})
}
