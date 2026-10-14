package aws_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRuntimeIgnoresContentType covers #1453: an InvokeAgent request whose
// JSON body comes with a form Content-Type (curl's default) or none at all
// reaches the agent runtime instead of the AWS-Query fallback, which answered
// it with InvalidAction XML.
func TestRuntimeIgnoresContentType(t *testing.T) {
	ts := fullAWSServer(t)

	status, resp := doRequest(t, ts, http.MethodPut, "/agents/", `{"agentName":"ct","foundationModel":"anthropic.claude-3-sonnet-20240229-v1:0"}`,
		map[string]string{"Content-Type": "application/json"})
	require.Equalf(t, http.StatusAccepted, status, "CreateAgent body=%s", resp)

	agentID := jsonField(t, resp, "agent", "agentId")

	status, resp = doRequest(t, ts, http.MethodPost, "/agents/"+agentID+"/", "", nil)
	require.Equalf(t, http.StatusAccepted, status, "PrepareAgent body=%s", resp)

	path := "/agents/" + agentID + "/agentAliases/TSTALIASID/sessions/s-1/text"

	for _, ct := range []string{"application/x-www-form-urlencoded", ""} {
		status, resp = doRequest(t, ts, http.MethodPost, path, `{"inputText":"hi"}`, map[string]string{"Content-Type": ct})
		assert.Equalf(t, http.StatusOK, status, "Content-Type %q: %s", ct, resp)
		assert.Falsef(t, strings.Contains(resp, "InvalidAction"), "Content-Type %q answered by the query fallback: %s", ct, resp)
		assert.Containsf(t, resp, "chunk", "Content-Type %q: an eventstream chunk", ct)
	}
}

// TestSessionTagsRoutedToRuntime covers #1453: a bedrock session ARN on the
// shared /tags path reaches the agent runtime, not EKS.
func TestSessionTagsRoutedToRuntime(t *testing.T) {
	ts := fullAWSServer(t)

	status, resp := doRequest(t, ts, http.MethodPut, "/sessions/", `{}`, map[string]string{"Content-Type": "application/json"})
	require.Equalf(t, http.StatusCreated, status, "CreateSession body=%s", resp)

	var arn string

	for _, part := range strings.Split(resp, `"`) {
		if strings.HasPrefix(part, "arn:aws:bedrock:") {
			arn = part
		}
	}

	require.NotEmpty(t, arn)

	status, resp = doRequest(t, ts, http.MethodPost, "/tags/"+arn, `{"tags":{"k":"v"}}`, map[string]string{"Content-Type": "application/json"})
	require.Equalf(t, http.StatusOK, status, "TagResource body=%s", resp)

	status, resp = doRequest(t, ts, http.MethodGet, "/tags/"+arn, "", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"tags":{"k":"v"}}`, resp)

	status, resp = doRequest(t, ts, http.MethodGet, "/tags/arn:aws:bedrock:us-east-1:123456789012:session/00000000-0000-4000-8000-000000000000", "", nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Contains(t, resp, "ResourceNotFoundException", "the runtime answers, not EKS")
}
