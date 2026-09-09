package kinesis_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// kinesisPost issues a raw Kinesis JSON 1.1 request against ts and returns the
// decoded top-level response object.
func kinesisPost(t *testing.T, url, target, body string) map[string]json.RawMessage {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("X-Amz-Target", "Kinesis_20131202."+target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s: %v", target, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d body %s", target, resp.StatusCode, raw)
	}

	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, raw)
	}

	return out
}

// TestEnableEnhancedMonitoringEmitsMetricArrays checks that the shard-level
// metric fields serialize as JSON arrays (empty when no metrics were previously
// set) rather than null, matching Kinesis's ShardLevelMetricsList shape.
func TestEnableEnhancedMonitoringEmitsMetricArrays(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Kinesis: cloud.Kinesis})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	kinesisPost(t, ts.URL, "CreateStream", `{"StreamName":"m","ShardCount":1}`)

	out := kinesisPost(t, ts.URL, "EnableEnhancedMonitoring",
		`{"StreamName":"m","ShardLevelMetrics":["IncomingBytes"]}`)

	if got := string(out["CurrentShardLevelMetrics"]); got != "[]" {
		t.Fatalf("CurrentShardLevelMetrics = %s, want []", got)
	}

	if got := string(out["DesiredShardLevelMetrics"]); got != `["IncomingBytes"]` {
		t.Fatalf("DesiredShardLevelMetrics = %s, want [\"IncomingBytes\"]", got)
	}
}
