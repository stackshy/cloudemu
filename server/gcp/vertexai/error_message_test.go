package vertexai_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVertexAIErrorMessagesOmitCodePrefix guards the FAILED_PRECONDITION path
// in server/gcp/vertexai (Predict on an endpoint with no deployed models)
// against baking cloudemu's internal cerrors code name (e.g.
// "FailedPrecondition: ...") into the wire error message an SDK caller sees.
// Real Vertex AI never prefixes its error messages with an internal
// error-taxonomy name.
func TestVertexAIErrorMessagesOmitCodePrefix(t *testing.T) {
	url := newServer(t)

	op := do(t, http.MethodPost, url+base+"/endpoints", map[string]any{"displayName": "ep"})
	epName := op["response"].(map[string]any)["name"].(string)

	reqBody, err := json.Marshal(map[string]any{
		"instances": []any{map[string]any{"x": 1}},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, url+"/v1/"+epName+":predict", bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), "body=%s", raw)

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:", "Internal:"} {
		if strings.Contains(out.Error.Message, prefix) {
			t.Errorf("wire error message %q leaks internal code prefix %q", out.Error.Message, prefix)
		}
	}
}
