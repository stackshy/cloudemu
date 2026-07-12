package azureai

import (
	"net/http"

	csdriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

func assistantJSON(a *csdriver.Assistant) map[string]any {
	return map[string]any{
		"id": a.ID, "object": "assistant", "model": a.Model, "name": a.Name,
		"instructions": a.Instructions, "created_at": a.CreatedAt,
	}
}

func threadJSON(t *csdriver.Thread) map[string]any {
	return map[string]any{"id": t.ID, "object": "thread", "created_at": t.CreatedAt}
}

func messageJSON(m *csdriver.ThreadMessage) map[string]any {
	return map[string]any{
		"id": m.ID, "object": "thread.message", "thread_id": m.ThreadID,
		"role": m.Role, "content": m.Content, "created_at": m.CreatedAt,
	}
}

func runJSON(r *csdriver.Run) map[string]any {
	return map[string]any{
		"id": r.ID, "object": "thread.run", "thread_id": r.ThreadID,
		"assistant_id": r.AssistantID, "status": r.Status, "created_at": r.CreatedAt,
	}
}

// serveAssistants handles /openai/assistants[/{id}].
func (h *DataPlaneHandler) serveAssistants(w http.ResponseWriter, r *http.Request, account string, parts []string) {
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPost:
			h.createAssistant(w, r, account)
		case http.MethodGet:
			h.listAssistants(w, r, account)
		default:
			dpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}

		return
	}

	id := parts[1]

	switch r.Method {
	case http.MethodGet:
		a, err := h.dp.GetAssistant(r.Context(), account, id)
		writeDP(w, assistantJSON, a, err)
	case http.MethodDelete:
		if err := h.dp.DeleteAssistant(r.Context(), account, id); err != nil {
			dpCErr(w, err)

			return
		}

		dpJSON(w, map[string]any{"id": id, "object": "assistant.deleted", "deleted": true})
	default:
		dpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *DataPlaneHandler) createAssistant(w http.ResponseWriter, r *http.Request, account string) {
	var body struct {
		Model        string `json:"model"`
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	a, err := h.dp.CreateAssistant(r.Context(), csdriver.AssistantConfig{
		Account: account, Model: body.Model, Name: body.Name, Instructions: body.Instructions,
	})
	writeDP(w, assistantJSON, a, err)
}

func (h *DataPlaneHandler) listAssistants(w http.ResponseWriter, r *http.Request, account string) {
	as, err := h.dp.ListAssistants(r.Context(), account)
	if err != nil {
		dpCErr(w, err)

		return
	}

	data := make([]map[string]any, 0, len(as))
	for i := range as {
		data = append(data, assistantJSON(&as[i]))
	}

	dpJSON(w, map[string]any{"object": "list", "data": data})
}

// serveThreads handles /openai/threads[/{id}[/messages|/runs[/{id}]]].
func (h *DataPlaneHandler) serveThreads(w http.ResponseWriter, r *http.Request, account string, parts []string) {
	if len(parts) == 1 {
		if r.Method != http.MethodPost {
			dpErr(w, http.StatusMethodNotAllowed, "method not allowed")

			return
		}

		_ = r.Body.Close()

		t, err := h.dp.CreateThread(r.Context(), account)
		writeDP(w, threadJSON, t, err)

		return
	}

	threadID := parts[1]

	const threadSubResIdx = 2
	if len(parts) > threadSubResIdx {
		switch parts[threadSubResIdx] {
		case "messages":
			h.serveMessages(w, r, account, threadID)
		case "runs":
			h.serveRuns(w, r, account, threadID, parts)
		default:
			dpErr(w, http.StatusNotFound, "unknown thread sub-resource: "+parts[2])
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		t, err := h.dp.GetThread(r.Context(), account, threadID)
		writeDP(w, threadJSON, t, err)
	case http.MethodDelete:
		if err := h.dp.DeleteThread(r.Context(), account, threadID); err != nil {
			dpCErr(w, err)

			return
		}

		dpJSON(w, map[string]any{"id": threadID, "object": "thread.deleted", "deleted": true})
	default:
		dpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *DataPlaneHandler) serveMessages(w http.ResponseWriter, r *http.Request, account, threadID string) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}

		if !dpDecode(w, r, &body) {
			return
		}

		msg, err := h.dp.CreateMessage(r.Context(), account, threadID, body.Role, body.Content)
		writeDP(w, messageJSON, msg, err)
	case http.MethodGet:
		msgs, err := h.dp.ListMessages(r.Context(), account, threadID)
		if err != nil {
			dpCErr(w, err)

			return
		}

		data := make([]map[string]any, 0, len(msgs))
		for i := range msgs {
			data = append(data, messageJSON(&msgs[i]))
		}

		dpJSON(w, map[string]any{"object": "list", "data": data})
	default:
		dpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *DataPlaneHandler) serveRuns(w http.ResponseWriter, r *http.Request, account, threadID string, parts []string) {
	// /openai/threads/{id}/runs/{runID}
	const runIDIdx = 3
	if len(parts) > runIDIdx {
		if r.Method != http.MethodGet {
			dpErr(w, http.StatusMethodNotAllowed, "method not allowed")

			return
		}

		run, err := h.dp.GetRun(r.Context(), account, threadID, parts[runIDIdx])
		writeDP(w, runJSON, run, err)

		return
	}

	if r.Method != http.MethodPost {
		dpErr(w, http.StatusMethodNotAllowed, "method not allowed")

		return
	}

	var body struct {
		AssistantID string `json:"assistant_id"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	run, err := h.dp.CreateRun(r.Context(), account, threadID, body.AssistantID)
	writeDP(w, runJSON, run, err)
}

// writeDP writes a created/fetched data-plane resource or maps the error.
func writeDP[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, res *T, err error) {
	if err != nil {
		dpCErr(w, err)

		return
	}

	dpJSON(w, toJSON(res))
}
