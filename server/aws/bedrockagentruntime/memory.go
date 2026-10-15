package bedrockagentruntime

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// serveGetMemory handles GetAgentMemory:
// GET /agents/{agentId}/agentAliases/{aliasId}/memories.
func serveGetMemory(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "GetAgentMemory", func(c driver.AgentMemory) {
		page, ok := queryPage(w, r, "maxItems")
		if !ok {
			return
		}

		q := r.URL.Query()

		summaries, next, err := c.GetAgentMemory(r.Context(), segs[1], segs[3], q.Get("memoryId"), q.Get("memoryType"), page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(summaries))
		for _, s := range summaries {
			out = append(out, map[string]any{"sessionSummary": map[string]string{
				"memoryId": s.MemoryID, "sessionId": s.SessionID, "summaryText": s.SummaryText,
				"sessionStartTime": s.SessionStartTime, "sessionExpiryTime": s.SessionExpiry,
			}})
		}

		writeJSON(w, map[string]any{"memoryContents": out, "nextToken": omitEmpty(next)})
	})
}

// serveDeleteMemory handles DeleteAgentMemory (202).
func serveDeleteMemory(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "DeleteAgentMemory", func(c driver.AgentMemory) {
		q := r.URL.Query()

		if err := c.DeleteAgentMemory(r.Context(), segs[1], segs[3], q.Get("memoryId"), q.Get("sessionId")); err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusAccepted, struct{}{})
	})
}
