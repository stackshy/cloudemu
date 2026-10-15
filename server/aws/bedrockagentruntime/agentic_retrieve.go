package bedrockagentruntime

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type agenticRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
	Retrievers []struct {
		Configuration struct {
			KnowledgeBase *struct {
				KnowledgeBaseID string `json:"knowledgeBaseId"`
			} `json:"knowledgeBase"`
		} `json:"configuration"`
	} `json:"retrievers"`
	GenerateResponse bool   `json:"generateResponse"`
	NextToken        string `json:"nextToken"`
}

// serveAgenticRetrieve handles AgenticRetrieveStream: a traceEvent per step,
// then the result event (and the answer as a responseEvent when asked).
func serveAgenticRetrieve(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	withCapability(h, w, "AgenticRetrieveStream", func(c driver.AgenticRetriever) {
		var in agenticRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		req := driver.AgenticRetrieveInput{GenerateResponse: in.GenerateResponse, NextToken: in.NextToken}
		for _, m := range in.Messages {
			req.Messages = append(req.Messages, m.Content.Text)
		}

		for _, rt := range in.Retrievers {
			if kb := rt.Configuration.KnowledgeBase; kb != nil {
				req.KnowledgeBaseIDs = append(req.KnowledgeBaseIDs, kb.KnowledgeBaseID)
			}
		}

		res, err := c.AgenticRetrieveStream(r.Context(), req)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeEventStream(w, nil, agenticEvents(res))
	})
}

func agenticEvents(res *driver.AgenticRetrieveResult) []streamEvent {
	events := make([]streamEvent, 0, len(res.Steps)+2) //nolint:mnd // the result and the response event.

	for i, s := range res.Steps {
		events = append(events, streamEvent{"traceEvent", map[string]any{
			"id": s.ID, "timestamp": int64(i),
			"attributes": map[string]string{"step": s.Step, "status": s.Status, "message": s.Message},
		}})
	}

	items := make([]map[string]any, 0, len(res.Results))
	for _, rr := range res.Results {
		items = append(items, map[string]any{
			"content":         map[string]string{"text": rr.Text, "mimeType": "text/plain"},
			"sourceRetriever": map[string]string{"identifier": rr.DocumentID},
			"metadata":        map[string]string{"score": strconv.FormatFloat(rr.Score, 'f', -1, 64)},
		})
	}

	result := map[string]any{"results": items, "nextToken": omitEmpty(res.NextToken)}
	if res.Answer != "" {
		result["generatedResponse"] = map[string]any{"answer": res.Answer, "citations": []any{}}
		events = append(events, streamEvent{"responseEvent", map[string]string{"text": res.Answer}})
	}

	return append(events, streamEvent{"result", result})
}
