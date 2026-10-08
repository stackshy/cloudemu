package sqs_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/sqs"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

var testScope = awsauthz.Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"} //nolint:gochecknoglobals // test fixture

const (
	q1ARN = "arn:aws:sqs:us-east-1:123456789012:q1"
	q1URL = "https://sqs.us-east-1.amazonaws.com/123456789012/q1"
)

func sqsRequest(op, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("X-Amz-Target", "AmazonSQS."+op)
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")

	return r
}

func TestIAMChecksQueueARNs(t *testing.T) {
	onQ1 := `{"QueueUrl":"` + q1URL + `"}`
	handle := base64.StdEncoding.EncodeToString([]byte(`{"taskId":"mmt-1","sourceArn":"` + q1ARN + `"}`))

	cases := []struct {
		op, body, action, resource string
	}{
		{"CreateQueue", `{"QueueName":"q1"}`, "sqs:CreateQueue", q1ARN},
		{"CreateQueue", `{"QueueName":"q1.fifo"}`, "sqs:CreateQueue", q1ARN + ".fifo"},
		{"GetQueueUrl", `{"QueueName":"q1"}`, "sqs:GetQueueUrl", q1ARN},
		{"ListQueues", `{}`, "sqs:ListQueues", "*"},
		{"DeleteQueue", onQ1, "sqs:DeleteQueue", q1ARN},
		{"SendMessage", onQ1, "sqs:SendMessage", q1ARN},
		{"SendMessageBatch", onQ1, "sqs:SendMessage", q1ARN},
		{"ReceiveMessage", onQ1, "sqs:ReceiveMessage", q1ARN},
		{"DeleteMessage", onQ1, "sqs:DeleteMessage", q1ARN},
		{"DeleteMessageBatch", onQ1, "sqs:DeleteMessage", q1ARN},
		{"ChangeMessageVisibility", onQ1, "sqs:ChangeMessageVisibility", q1ARN},
		{"ChangeMessageVisibilityBatch", onQ1, "sqs:ChangeMessageVisibility", q1ARN},
		{"ListDeadLetterSourceQueues", onQ1, "sqs:ListDeadLetterSourceQueues", q1ARN},
		{"GetQueueAttributes", onQ1, "sqs:GetQueueAttributes", q1ARN},
		{"SetQueueAttributes", onQ1, "sqs:SetQueueAttributes", q1ARN},
		{"PurgeQueue", onQ1, "sqs:PurgeQueue", q1ARN},
		{"TagQueue", onQ1, "sqs:TagQueue", q1ARN},
		{"UntagQueue", onQ1, "sqs:UntagQueue", q1ARN},
		{"ListQueueTags", onQ1, "sqs:ListQueueTags", q1ARN},
		{"AddPermission", onQ1, "sqs:AddPermission", q1ARN},
		{"RemovePermission", onQ1, "sqs:RemovePermission", q1ARN},
		{"StartMessageMoveTask", `{"SourceArn":"` + q1ARN + `"}`, "sqs:StartMessageMoveTask", q1ARN},
		{"ListMessageMoveTasks", `{"SourceArn":"` + q1ARN + `"}`, "sqs:ListMessageMoveTasks", q1ARN},
		{"CancelMessageMoveTask", `{"TaskHandle":"` + handle + `"}`, "sqs:CancelMessageMoveTask", q1ARN},
		// The account and region always come from the server, never the URL.
		{"SendMessage", `{"QueueUrl":"https://sqs.eu-west-1.amazonaws.com/999999999999/q1"}`, "sqs:SendMessage", q1ARN},
		{"StartMessageMoveTask", `{"SourceArn":"arn:aws:sqs:eu-west-1:999999999999:q1"}`, "sqs:StartMessageMoveTask", q1ARN},
		// A queue the request does not name cleanly stays unknown.
		{"SendMessage", `{}`, "sqs:SendMessage", ""},
		{"SendMessage", `{"QueueUrl":"https://sqs.us-east-1.amazonaws.com/123456789012/"}`, "sqs:SendMessage", ""},
		{"SendMessage", `{"QueueUrl":"https://sqs.us-east-1.amazonaws.com/123456789012/q*"}`, "sqs:SendMessage", ""},
		{"CreateQueue", `{"QueueName":"bad name"}`, "sqs:CreateQueue", ""},
		{"StartMessageMoveTask", `{"SourceArn":"q1"}`, "sqs:StartMessageMoveTask", ""},
		{"CancelMessageMoveTask", `{"TaskHandle":"not-base64!"}`, "sqs:CancelMessageMoveTask", ""},
		{"SendMessage", `{"QueueUrl":`, "sqs:SendMessage", ""},
	}

	h := sqs.New(nil)

	for _, tc := range cases {
		r := sqsRequest(tc.op, tc.body)

		checks, ok := h.IAMChecks(r, testScope)
		if !ok || len(checks) == 0 || checks[0].Action != tc.action || checks[0].Resource != tc.resource {
			t.Errorf("%s %s: got %+v ok=%v, want %s on %q", tc.op, tc.body, checks, ok, tc.action, tc.resource)
		}

		if rest, _ := io.ReadAll(r.Body); string(rest) != tc.body {
			t.Errorf("%s: body not restored: %q", tc.op, rest)
		}
	}
}

// TestIAMChecksMoveTasks: a redrive needs the redrive actions on the source
// queue and sqs:SendMessage on the destination, which is unknown when
// DestinationArn is empty (messages go back to their original queues).
func TestIAMChecksMoveTasks(t *testing.T) {
	const q2ARN = "arn:aws:sqs:us-east-1:123456789012:q2"

	handle := base64.StdEncoding.EncodeToString([]byte(`{"taskId":"mmt-1","sourceArn":"` + q1ARN + `"}`))
	src := func(actions ...string) []string {
		out := make([]string, 0, len(actions))
		for _, a := range actions {
			out = append(out, "sqs:"+a+" "+q1ARN)
		}

		return out
	}

	cases := []struct {
		op, body string
		want     []string
	}{
		{"StartMessageMoveTask", `{"SourceArn":"` + q1ARN + `","DestinationArn":"` + q2ARN + `"}`,
			append(src("StartMessageMoveTask", "ReceiveMessage", "DeleteMessage", "GetQueueAttributes"), "sqs:SendMessage "+q2ARN)},
		{"StartMessageMoveTask", `{"SourceArn":"` + q1ARN + `"}`,
			append(src("StartMessageMoveTask", "ReceiveMessage", "DeleteMessage", "GetQueueAttributes"), "sqs:SendMessage ")},
		{"StartMessageMoveTask", `{"SourceArn":"` + q1ARN + `","DestinationArn":"q2"}`,
			append(src("StartMessageMoveTask", "ReceiveMessage", "DeleteMessage", "GetQueueAttributes"), "sqs:SendMessage ")},
		{"CancelMessageMoveTask", `{"TaskHandle":"` + handle + `"}`,
			src("CancelMessageMoveTask", "ReceiveMessage", "DeleteMessage", "GetQueueAttributes")},
		{"ListMessageMoveTasks", `{"SourceArn":"` + q1ARN + `"}`, src("ListMessageMoveTasks", "GetQueueAttributes")},
	}

	h := sqs.New(nil)

	for _, tc := range cases {
		checks, ok := h.IAMChecks(sqsRequest(tc.op, tc.body), testScope)

		got := make([]string, 0, len(checks))
		for _, c := range checks {
			got = append(got, c.Action+" "+c.Resource)
		}

		if !ok || strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s %s:\n got %v\nwant %v", tc.op, tc.body, got, tc.want)
		}
	}
}

func TestIAMChecksUnknownOperation(t *testing.T) {
	h := sqs.New(nil)

	for _, op := range []string{"", "Bogus", "sendmessage"} {
		if checks, ok := h.IAMChecks(sqsRequest(op, `{}`), testScope); ok {
			t.Errorf("%q: got %+v, want ok=false", op, checks)
		}
	}
}
