package batch_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stackshy/cloudemu/v2/server/aws/batch"
)

// TestMatchesTagsARNScoping guards that the Batch handler claims the shared
// /v1/tags/{arn} path only for batch ARNs. MSK's tag operations live at the same
// path; without this scoping Batch (registered first) steals every tag request.
func TestMatchesTagsARNScoping(t *testing.T) {
	h := batch.New(cloudemu.NewAWS().Batch)

	batchARN := "arn:aws:batch:us-east-1:123456789012:compute-environment/env1"
	kafkaARN := "arn:aws:kafka:us-east-1:123456789012:cluster/demo/abc-123"

	if !h.Matches(httptest.NewRequest(http.MethodGet, "/v1/tags/"+batchARN, nil)) {
		t.Errorf("Batch must claim /v1/tags/<batch-arn>")
	}

	if h.Matches(httptest.NewRequest(http.MethodGet, "/v1/tags/"+kafkaARN, nil)) {
		t.Errorf("Batch must NOT claim /v1/tags/<kafka-arn> (would steal MSK tagging)")
	}
}

// TestTagsDoNotStealMSK confirms that on a server with BOTH Batch and Kafka
// registered, a kafka tag ARN is not answered by the Batch handler — the
// regression that broke MSK tagging when Batch was added.
func TestTagsDoNotStealMSK(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Batch: cloud.Batch, Kafka: cloud.Kafka}))
	t.Cleanup(ts.Close)

	kafkaARN := "arn:aws:kafka:us-east-1:123456789012:cluster/demo/abc-123"

	resp, err := http.Get(ts.URL + "/v1/tags/" + kafkaARN)
	if err != nil {
		t.Fatalf("GET kafka tags: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	// Batch's ARN parser rejects a kafka ARN with this exact message; seeing it
	// means Batch wrongly claimed the request instead of MSK.
	if strings.Contains(body, "unsupported resource ARN") {
		t.Fatalf("Batch stole the MSK tag request: %s", body)
	}
}
