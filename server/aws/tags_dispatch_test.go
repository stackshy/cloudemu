package aws_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSharedTagsPathRoutedByARN covers the shared /tags/{resourceArn} REST
// path. bedrock-agent claims it only for its own ARNs and registers before
// EKS, whose Matches claims every /tags request. A bedrock-agent ARN must reach
// bedrock-agent and an EKS cluster ARN must still reach EKS.
func TestSharedTagsPathRoutedByARN(t *testing.T) {
	ts := fullAWSServer(t)
	jsonCT := map[string]string{"Content-Type": "application/json"}

	status, resp := doRequest(t, ts, http.MethodPut, "/agents/",
		`{"agentName":"tagged","tags":{"owner":"bedrock"}}`, jsonCT)
	require.Equalf(t, http.StatusOK, status, "CreateAgent body=%s", resp)

	agentARN := jsonField(t, resp, "agent", "agentArn")

	status, resp = doRequest(t, ts, http.MethodGet, "/tags/"+agentARN, "", nil)
	assert.Equalf(t, http.StatusOK, status, "bedrock-agent ListTagsForResource body=%s", resp)
	assert.JSONEqf(t, `{"tags":{"owner":"bedrock"}}`, resp, "bedrock-agent ARN answered by the wrong handler")

	status, resp = doRequest(t, ts, http.MethodPost, "/clusters",
		`{"name":"c1","roleArn":"arn:aws:iam::123456789012:role/eks","tags":{"owner":"eks"}}`, jsonCT)
	require.Equalf(t, http.StatusOK, status, "CreateCluster body=%s", resp)

	clusterARN := jsonField(t, resp, "cluster", "arn")

	status, resp = doRequest(t, ts, http.MethodGet, "/tags/"+clusterARN, "", nil)
	assert.Equalf(t, http.StatusOK, status, "EKS ListTagsForResource body=%s", resp)
	assert.JSONEqf(t, `{"tags":{"owner":"eks"}}`, resp, "EKS ARN answered by the wrong handler")
}

func jsonField(t *testing.T, body, obj, field string) string {
	t.Helper()

	var out map[string]map[string]any

	require.NoError(t, json.Unmarshal([]byte(body), &out))

	v, _ := out[obj][field].(string)
	require.NotEmptyf(t, v, "%s.%s missing in %s", obj, field, body)

	return v
}
