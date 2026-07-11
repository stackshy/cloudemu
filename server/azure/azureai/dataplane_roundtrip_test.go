package azureai_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

func newDPServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{AzureAIDataPlane: cloud.AzureAI})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func TestChatCompletions(t *testing.T) {
	url := newDPServer(t)

	resp := do(t, http.MethodPost, url+"/openai/deployments/gpt4o/chat/completions", map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "user", "content": "hello there world"},
		},
	})

	assert.Equal(t, "chat.completion", resp["object"])
	choices := resp["choices"].([]any)
	require.Len(t, choices, 1)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	assert.Equal(t, "assistant", msg["role"])
	assert.Contains(t, msg["content"], "hello there world")

	usage := resp["usage"].(map[string]any)
	assert.Greater(t, usage["total_tokens"], float64(0))
}

func TestEmbeddings(t *testing.T) {
	url := newDPServer(t)

	resp := do(t, http.MethodPost, url+"/openai/deployments/embed/embeddings", map[string]any{
		"input": []any{"alpha", "beta"},
	})

	data := resp["data"].([]any)
	require.Len(t, data, 2)
	vec := data[0].(map[string]any)["embedding"].([]any)
	assert.NotEmpty(t, vec)
}

func TestCompletions(t *testing.T) {
	url := newDPServer(t)

	resp := do(t, http.MethodPost, url+"/openai/deployments/gpt35/completions", map[string]any{
		"prompt": "say hi",
	})

	choices := resp["choices"].([]any)
	require.Len(t, choices, 1)
	assert.Contains(t, choices[0].(map[string]any)["text"], "say hi")
}

func TestAssistantsThreadsRuns(t *testing.T) {
	url := newDPServer(t)

	asst := do(t, http.MethodPost, url+"/openai/assistants", map[string]any{
		"model": "gpt4o", "name": "helper", "instructions": "help",
	})
	asstID := asst["id"].(string)
	require.NotEmpty(t, asstID)

	list := do(t, http.MethodGet, url+"/openai/assistants", nil)
	assert.Len(t, list["data"], 1)

	thread := do(t, http.MethodPost, url+"/openai/threads", map[string]any{})
	threadID := thread["id"].(string)
	require.NotEmpty(t, threadID)

	msg := do(t, http.MethodPost, url+"/openai/threads/"+threadID+"/messages", map[string]any{
		"role": "user", "content": "question?",
	})
	assert.Equal(t, "question?", msg["content"])

	msgs := do(t, http.MethodGet, url+"/openai/threads/"+threadID+"/messages", nil)
	assert.Len(t, msgs["data"], 1)

	run := do(t, http.MethodPost, url+"/openai/threads/"+threadID+"/runs", map[string]any{
		"assistant_id": asstID,
	})
	runID := run["id"].(string)
	assert.Equal(t, "completed", run["status"])

	gotRun := do(t, http.MethodGet, url+"/openai/threads/"+threadID+"/runs/"+runID, nil)
	assert.Equal(t, runID, gotRun["id"])

	// Cleanup paths.
	do(t, http.MethodDelete, url+"/openai/assistants/"+asstID, nil)
	do(t, http.MethodDelete, url+"/openai/threads/"+threadID, nil)
}

func TestScore(t *testing.T) {
	url := newDPServer(t)

	resp := do(t, http.MethodPost, url+"/score", map[string]any{
		"input_data": map[string]any{"data": []any{[]any{1, 2, 3}}},
	})
	require.NotNil(t, resp["input_data"])
}

func TestListMessagesOrdered(t *testing.T) {
	url := newDPServer(t)

	thread := do(t, http.MethodPost, url+"/openai/threads", map[string]any{})
	threadID := thread["id"].(string)

	want := []string{"m0", "m1", "m2", "m3", "m4"}
	for _, c := range want {
		do(t, http.MethodPost, url+"/openai/threads/"+threadID+"/messages", map[string]any{"role": "user", "content": c})
	}

	// Repeated listings must return a stable creation-ordered sequence.
	for range 3 {
		msgs := do(t, http.MethodGet, url+"/openai/threads/"+threadID+"/messages", nil)
		data := msgs["data"].([]any)
		require.Len(t, data, len(want))

		got := make([]string, len(data))
		for i, d := range data {
			got[i] = d.(map[string]any)["content"].(string)
		}

		assert.Equal(t, want, got)
	}
}
