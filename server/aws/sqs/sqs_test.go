package sqs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server/aws/sqs"
)

func newServer(t *testing.T) (*httptest.Server, mqHandle) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := httptest.NewServer(sqs.New(cloud.SQS))
	t.Cleanup(srv.Close)

	return srv, mqHandle{queueURL: ""}
}

type mqHandle struct {
	queueURL string
}

func TestMatchesAcceptsAmazonSQSTarget(t *testing.T) {
	h := sqs.New(nil)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")

	if !h.Matches(req) {
		t.Fatal("Matches should accept AmazonSQS.* target")
	}
}

func TestMatchesRejectsDynamoDBTarget(t *testing.T) {
	h := sqs.New(nil)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")

	if h.Matches(req) {
		t.Fatal("Matches should reject DynamoDB target")
	}
}

func TestCreateAndGetQueueURL(t *testing.T) {
	srv, _ := newServer(t)

	createBody := `{"QueueName":"q1"}`
	create := postJSON(t, srv, "AmazonSQS.CreateQueue", createBody)

	if create.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", create.StatusCode)
	}

	resp := postJSON(t, srv, "AmazonSQS.GetQueueUrl", `{"QueueName":"q1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, `"QueueUrl"`) || !strings.Contains(body, "q1") {
		t.Fatalf("response missing QueueUrl: %s", body)
	}
}

func TestGetQueueURLMissing(t *testing.T) {
	srv, _ := newServer(t)

	resp := postJSON(t, srv, "AmazonSQS.GetQueueUrl", `{"QueueName":"nope"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	if !strings.Contains(readBody(t, resp), "QueueDoesNotExist") {
		t.Fatal("expected QueueDoesNotExist error")
	}
}

func TestSendReceiveDelete(t *testing.T) {
	srv, _ := newServer(t)

	create := postJSON(t, srv, "AmazonSQS.CreateQueue", `{"QueueName":"loop"}`)
	queueURL := extractQueueURL(t, create)

	send := postJSON(t, srv, "AmazonSQS.SendMessage",
		`{"QueueUrl":"`+queueURL+`","MessageBody":"hello"}`)
	if send.StatusCode != http.StatusOK {
		t.Fatalf("send status = %d", send.StatusCode)
	}

	recv := postJSON(t, srv, "AmazonSQS.ReceiveMessage",
		`{"QueueUrl":"`+queueURL+`","MaxNumberOfMessages":1}`)

	body := readBody(t, recv)
	if !strings.Contains(body, "hello") {
		t.Fatalf("Receive missing payload: %s", body)
	}

	receipt := extractReceipt(body)
	if receipt == "" {
		t.Fatalf("no ReceiptHandle in: %s", body)
	}

	del := postJSON(t, srv, "AmazonSQS.DeleteMessage",
		`{"QueueUrl":"`+queueURL+`","ReceiptHandle":"`+receipt+`"}`)
	if del.StatusCode != http.StatusOK {
		t.Fatalf("delete msg status = %d", del.StatusCode)
	}
}

func TestReceiveEmptyQueue(t *testing.T) {
	srv, _ := newServer(t)

	create := postJSON(t, srv, "AmazonSQS.CreateQueue", `{"QueueName":"empty"}`)
	queueURL := extractQueueURL(t, create)

	resp := postJSON(t, srv, "AmazonSQS.ReceiveMessage",
		`{"QueueUrl":"`+queueURL+`"}`)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	// Empty receive should return Messages: [] or omit.
	if strings.Contains(body, "ReceiptHandle") {
		t.Fatalf("expected no messages, got %s", body)
	}
}

func TestListQueues(t *testing.T) {
	srv, _ := newServer(t)

	for _, n := range []string{"a", "b", "c"} {
		_ = postJSON(t, srv, "AmazonSQS.CreateQueue", `{"QueueName":"`+n+`"}`)
	}

	resp := postJSON(t, srv, "AmazonSQS.ListQueues", `{}`)
	body := readBody(t, resp)

	for _, n := range []string{"a", "b", "c"} {
		if !strings.Contains(body, "/"+n+`"`) {
			t.Fatalf("missing queue %q in list response: %s", n, body)
		}
	}
}

func TestDeleteQueue(t *testing.T) {
	srv, _ := newServer(t)

	create := postJSON(t, srv, "AmazonSQS.CreateQueue", `{"QueueName":"goner"}`)
	queueURL := extractQueueURL(t, create)

	del := postJSON(t, srv, "AmazonSQS.DeleteQueue",
		`{"QueueUrl":"`+queueURL+`"}`)
	if del.StatusCode != http.StatusOK {
		t.Fatalf("delete queue status = %d", del.StatusCode)
	}

	resp := postJSON(t, srv, "AmazonSQS.GetQueueUrl", `{"QueueName":"goner"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("post-delete get-url = %d, want 400", resp.StatusCode)
	}
}

func TestUnknownOperation(t *testing.T) {
	srv, _ := newServer(t)

	resp := postJSON(t, srv, "AmazonSQS.SomeMadeUpOp", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// helpers --------------------------------------------------------------------

// TestQueueAttributesAndPurge is a regression guard for issue #319: the SQS
// handler previously did not dispatch GetQueueAttributes/SetQueueAttributes/
// PurgeQueue, so callers couldn't read a queue's ARN (needed for DLQ wiring,
// event-source mappings, and S3->SQS notifications) or resize/drain a queue.
func TestQueueAttributesAndPurge(t *testing.T) {
	srv, _ := newServer(t)

	create := postJSON(t, srv, "AmazonSQS.CreateQueue", `{"QueueName":"attrq"}`)
	qurl := extractQueueURL(t, create)

	// GetQueueAttributes(All) must surface QueueArn.
	got := readBody(t, postJSON(t, srv, "AmazonSQS.GetQueueAttributes",
		`{"QueueUrl":"`+qurl+`","AttributeNames":["All"]}`))
	if !strings.Contains(got, `"QueueArn":"arn:aws:sqs:`) {
		t.Fatalf("GetQueueAttributes missing QueueArn: %s", got)
	}

	// SetQueueAttributes persists a numeric attribute.
	if resp := postJSON(t, srv, "AmazonSQS.SetQueueAttributes",
		`{"QueueUrl":"`+qurl+`","Attributes":{"VisibilityTimeout":"45"}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("SetQueueAttributes status = %d", resp.StatusCode)
	}

	got = readBody(t, postJSON(t, srv, "AmazonSQS.GetQueueAttributes",
		`{"QueueUrl":"`+qurl+`","AttributeNames":["VisibilityTimeout"]}`))
	if !strings.Contains(got, `"VisibilityTimeout":"45"`) {
		t.Fatalf("SetQueueAttributes not applied: %s", got)
	}

	// PurgeQueue drains messages.
	postJSON(t, srv, "AmazonSQS.SendMessage", `{"QueueUrl":"`+qurl+`","MessageBody":"x"}`)
	if resp := postJSON(t, srv, "AmazonSQS.PurgeQueue",
		`{"QueueUrl":"`+qurl+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PurgeQueue status = %d", resp.StatusCode)
	}

	got = readBody(t, postJSON(t, srv, "AmazonSQS.GetQueueAttributes",
		`{"QueueUrl":"`+qurl+`","AttributeNames":["ApproximateNumberOfMessages"]}`))
	if !strings.Contains(got, `"ApproximateNumberOfMessages":"0"`) {
		t.Fatalf("PurgeQueue left messages: %s", got)
	}
}

// TestQueueTagging is a regression guard for issue #319: TagQueue /
// UntagQueue / ListQueueTags were unimplemented (UnknownOperationException).
func TestQueueTagging(t *testing.T) {
	srv, _ := newServer(t)

	create := postJSON(t, srv, "AmazonSQS.CreateQueue", `{"QueueName":"tq"}`)
	qurl := extractQueueURL(t, create)

	if resp := postJSON(t, srv, "AmazonSQS.TagQueue",
		`{"QueueUrl":"`+qurl+`","Tags":{"env":"prod","team":"msg"}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("TagQueue status = %d", resp.StatusCode)
	}

	got := readBody(t, postJSON(t, srv, "AmazonSQS.ListQueueTags", `{"QueueUrl":"`+qurl+`"}`))
	if !strings.Contains(got, `"env":"prod"`) || !strings.Contains(got, `"team":"msg"`) {
		t.Fatalf("ListQueueTags = %s", got)
	}

	if resp := postJSON(t, srv, "AmazonSQS.UntagQueue",
		`{"QueueUrl":"`+qurl+`","TagKeys":["env"]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("UntagQueue status = %d", resp.StatusCode)
	}

	got = readBody(t, postJSON(t, srv, "AmazonSQS.ListQueueTags", `{"QueueUrl":"`+qurl+`"}`))
	if strings.Contains(got, `"env"`) || !strings.Contains(got, `"team":"msg"`) {
		t.Fatalf("after untag = %s", got)
	}
}

func postJSON(t *testing.T, srv *httptest.Server, target, body string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, srv.URL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}

	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)

	return string(buf[:n])
}

func extractQueueURL(t *testing.T, resp *http.Response) string {
	t.Helper()

	body := readBody(t, resp)
	const key = `"QueueUrl":"`
	i := strings.Index(body, key)
	if i < 0 {
		t.Fatalf("QueueUrl not in body: %s", body)
	}
	rest := body[i+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("malformed QueueUrl in body: %s", body)
	}

	return rest[:end]
}

func extractReceipt(body string) string {
	const key = `"ReceiptHandle":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
