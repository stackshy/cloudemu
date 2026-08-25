package s3

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// recordingPublisher captures S3 -> SNS deliveries.
type recordingPublisher struct {
	arns   []string
	bodies []string
}

func (p *recordingPublisher) PublishExternal(_ context.Context, topicARN, message string) error {
	p.arns = append(p.arns, topicARN)
	p.bodies = append(p.bodies, message)

	return nil
}

// recordingInvoker captures S3 -> Lambda deliveries.
type recordingInvoker struct {
	arns     []string
	payloads []string
}

func (i *recordingInvoker) InvokeExternal(_ context.Context, functionARN string, payload []byte) error {
	i.arns = append(i.arns, functionARN)
	i.payloads = append(i.payloads, string(payload))

	return nil
}

// TestNotificationFilterAndTargets verifies that S3 event delivery honors the
// S3Key prefix/suffix filter (no over-delivery) and dispatches to SQS, SNS, and
// Lambda targets by kind and event selector.
func TestNotificationFilterAndTargets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sqs := &recordingDeliverer{}
	sns := &recordingPublisher{}
	lambda := &recordingInvoker{}
	m.SetSQSDeliverer(sqs)
	m.SetSNSPublisher(sns)
	m.SetLambdaInvoker(lambda)

	if err := m.CreateBucket(ctx, "nb"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	const (
		queueARN  = "arn:aws:sqs:us-east-1:000000000000:s3events"
		topicARN  = "arn:aws:sns:us-east-1:000000000000:s3topic"
		lambdaARN = "arn:aws:lambda:us-east-1:000000000000:function:s3fn"
	)

	if err := m.PutBucketNotification(ctx, "nb", []BucketNotification{
		{
			Target: NotifyQueue, ARN: queueARN, Events: []string{"s3:ObjectCreated:*"},
			Filters: []NotificationFilterRule{
				{Name: "prefix", Value: "images/"},
				{Name: "suffix", Value: ".jpg"},
			},
		},
		{Target: NotifyTopic, ARN: topicARN, Events: []string{"s3:ObjectCreated:*"}},
		{Target: NotifyLambda, ARN: lambdaARN, Events: []string{"s3:ObjectRemoved:*"}},
	}); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}

	// A matching key: reaches the filtered queue and the (unfiltered) topic, not
	// the lambda (event mismatch).
	if err := m.PutObject(ctx, "nb", "images/a.jpg", []byte("x"), "image/jpeg", nil); err != nil {
		t.Fatalf("PutObject match: %v", err)
	}

	if len(sqs.arns) != 1 || sqs.arns[0] != queueARN {
		t.Fatalf("queue deliveries = %v, want one to %s", sqs.arns, queueARN)
	}

	if len(sns.arns) != 1 || sns.arns[0] != topicARN {
		t.Fatalf("topic deliveries = %v, want one to %s", sns.arns, topicARN)
	}

	if len(lambda.arns) != 0 {
		t.Fatalf("lambda deliveries = %v, want none on create", lambda.arns)
	}

	// A key failing the filter (wrong prefix and suffix): the queue must NOT
	// receive it (regression: events were delivered for every key), but the
	// unfiltered topic still does.
	if err := m.PutObject(ctx, "nb", "docs/b.txt", []byte("y"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject non-match: %v", err)
	}

	if len(sqs.arns) != 1 {
		t.Fatalf("queue over-delivered to a filtered-out key: %v", sqs.arns)
	}

	if len(sns.arns) != 2 {
		t.Fatalf("topic deliveries = %v, want two", sns.arns)
	}

	// Removing an object fires the lambda target (ObjectRemoved:*).
	if err := m.DeleteObject(ctx, "nb", "images/a.jpg"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	if len(lambda.arns) != 1 || lambda.arns[0] != lambdaARN {
		t.Fatalf("lambda deliveries = %v, want one to %s", lambda.arns, lambdaARN)
	}

	if !strings.Contains(lambda.payloads[0], `"eventName":"ObjectRemoved:Delete"`) {
		t.Fatalf("lambda payload = %s", lambda.payloads[0])
	}
}

// TestObjectEventFullShape verifies the S3 event record delivered to a target
// carries the complete documented shape — eventVersion, userIdentity,
// requestParameters, responseElements, and the full s3 block with
// s3SchemaVersion, configurationId, bucket.{name,arn,ownerIdentity} and
// object.{key,size,eTag,sequencer} — not just bucket.name/object.key.
func TestObjectEventFullShape(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	lambda := &recordingInvoker{}
	m.SetLambdaInvoker(lambda)

	if err := m.CreateBucket(ctx, "shape-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := m.PutBucketNotification(ctx, "shape-bucket", []BucketNotification{
		{ID: "cfg-1", Target: NotifyLambda, ARN: "arn:aws:lambda:us-east-1:0:function:f", Events: []string{"s3:ObjectCreated:*"}},
	}); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}

	if err := m.PutObject(ctx, "shape-bucket", "uploads/photo.jpg", []byte("data"), "image/jpeg", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if len(lambda.payloads) != 1 {
		t.Fatalf("lambda invocations = %d, want 1", len(lambda.payloads))
	}

	var event struct {
		Records []struct {
			EventVersion      string         `json:"eventVersion"`
			EventSource       string         `json:"eventSource"`
			EventName         string         `json:"eventName"`
			UserIdentity      map[string]any `json:"userIdentity"`
			RequestParameters map[string]any `json:"requestParameters"`
			ResponseElements  map[string]any `json:"responseElements"`
			S3                struct {
				SchemaVersion   string `json:"s3SchemaVersion"`
				ConfigurationID string `json:"configurationId"`
				Bucket          struct {
					Name          string         `json:"name"`
					ARN           string         `json:"arn"`
					OwnerIdentity map[string]any `json:"ownerIdentity"`
				} `json:"bucket"`
				Object struct {
					Key       string `json:"key"`
					Size      int64  `json:"size"`
					ETag      string `json:"eTag"`
					Sequencer string `json:"sequencer"`
				} `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}

	if err := json.Unmarshal([]byte(lambda.payloads[0]), &event); err != nil {
		t.Fatalf("unmarshal event: %v\npayload=%s", err, lambda.payloads[0])
	}

	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(event.Records))
	}

	r := event.Records[0]
	assertEq(t, "eventVersion", r.EventVersion, "2.1")
	assertEq(t, "eventSource", r.EventSource, "aws:s3")
	assertEq(t, "eventName", r.EventName, "ObjectCreated:Put")

	if r.UserIdentity["principalId"] == "" || r.UserIdentity["principalId"] == nil {
		t.Fatalf("userIdentity.principalId empty: %+v", r.UserIdentity)
	}

	if r.RequestParameters["sourceIPAddress"] == nil {
		t.Fatalf("requestParameters.sourceIPAddress missing: %+v", r.RequestParameters)
	}

	if r.ResponseElements["x-amz-request-id"] == nil {
		t.Fatalf("responseElements.x-amz-request-id missing: %+v", r.ResponseElements)
	}

	assertEq(t, "s3SchemaVersion", r.S3.SchemaVersion, "1.0")
	assertEq(t, "configurationId", r.S3.ConfigurationID, "cfg-1")
	assertEq(t, "bucket.name", r.S3.Bucket.Name, "shape-bucket")
	assertEq(t, "bucket.arn", r.S3.Bucket.ARN, "arn:aws:s3:::shape-bucket")

	if r.S3.Bucket.OwnerIdentity["principalId"] == nil {
		t.Fatalf("bucket.ownerIdentity.principalId missing: %+v", r.S3.Bucket.OwnerIdentity)
	}

	assertEq(t, "object.key", r.S3.Object.Key, "uploads/photo.jpg")

	if r.S3.Object.Size != 4 {
		t.Fatalf("object.size = %d, want 4", r.S3.Object.Size)
	}

	if len(r.S3.Object.ETag) != 32 {
		t.Fatalf("object.eTag = %q, want a 32-char md5", r.S3.Object.ETag)
	}

	if r.S3.Object.Sequencer == "" {
		t.Fatalf("object.sequencer empty")
	}
}

func assertEq(t *testing.T, field, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

// TestNotificationConfigRoundTrip verifies Put/Get preserves every target kind
// and its filter rules.
func TestNotificationConfigRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "rb"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	want := []BucketNotification{
		{
			ID: "q", Target: NotifyQueue, ARN: "arn:aws:sqs:us-east-1:0:q", Events: []string{"s3:ObjectCreated:Put"},
			Filters: []NotificationFilterRule{{Name: "prefix", Value: "in/"}},
		},
		{ID: "t", Target: NotifyTopic, ARN: "arn:aws:sns:us-east-1:0:t", Events: []string{"s3:ObjectCreated:*"}},
		{ID: "l", Target: NotifyLambda, ARN: "arn:aws:lambda:us-east-1:0:function:f", Events: []string{"s3:ObjectRemoved:*"}},
	}

	if err := m.PutBucketNotification(ctx, "rb", want); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}

	got, err := m.GetBucketNotification(ctx, "rb")
	if err != nil {
		t.Fatalf("GetBucketNotification: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d configs, want 3", len(got))
	}

	if got[0].Target != NotifyQueue || len(got[0].Filters) != 1 || got[0].Filters[0].Value != "in/" {
		t.Fatalf("queue config not round-tripped: %+v", got[0])
	}

	if got[1].Target != NotifyTopic || got[2].Target != NotifyLambda {
		t.Fatalf("topic/lambda configs not round-tripped: %+v", got)
	}
}
