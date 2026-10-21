package eventbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// TestSDKPutEventsEntryIDsAlignWithRequest checks that each result entry belongs
// to the request entry at the same index. An entry for a missing bus succeeds
// with its own EventId (real EventBridge drops it silently), and the EventId at
// a later index is the id of the event that was actually delivered.
func TestSDKPutEventsEntryIDsAlignWithRequest(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-align")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("r"), EventPattern: aws.String(`{"source":["my.app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"), Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	out, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("my.app"), DetailType: aws.String("t"), Detail: aws.String("{}"), EventBusName: aws.String("ghost")},
		{DetailType: aws.String("t"), Detail: aws.String("{}")},
		{Source: aws.String("my.app"), DetailType: aws.String("t"), Detail: aws.String(`{"n":1}`)},
	}})
	if err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	if out.FailedEntryCount != 1 || len(out.Entries) != 3 {
		t.Fatalf("FailedEntryCount=%d entries=%d, want 1 and 3", out.FailedEntryCount, len(out.Entries))
	}

	if aws.ToString(out.Entries[0].EventId) == "" || out.Entries[0].ErrorCode != nil {
		t.Fatalf("entry 0 (missing bus) = %+v, want an EventId and no error", out.Entries[0])
	}

	if aws.ToString(out.Entries[1].ErrorCode) != "InvalidArgument" {
		t.Fatalf("entry 1 ErrorCode = %q, want InvalidArgument", aws.ToString(out.Entries[1].ErrorCode))
	}

	body, n := receiveOne(t, sqs, url)
	if n != 1 {
		t.Fatalf("delivered %d messages, want 1", n)
	}

	var env struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}

	if got := aws.ToString(out.Entries[2].EventId); got != env.ID {
		t.Fatalf("entry 2 EventId = %q, delivered event id = %q", got, env.ID)
	}
}

func TestSDKPutEventsRequiredFields(t *testing.T) {
	client := newEventBridgeClient(t)

	out, err := client.PutEvents(context.Background(), &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{DetailType: aws.String("t"), Detail: aws.String("{}")},
		{Source: aws.String("s"), Detail: aws.String("{}")},
		{Source: aws.String("s"), DetailType: aws.String("t")},
	}})
	if err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	want := []string{
		"Parameter Source is not valid. Reason: Source is a required argument.",
		"Parameter DetailType is not valid. Reason: DetailType is a required argument.",
		"Parameter Detail is not valid. Reason: Detail is a required argument.",
	}

	if out.FailedEntryCount != 3 {
		t.Fatalf("FailedEntryCount = %d, want 3", out.FailedEntryCount)
	}

	for i, msg := range want {
		e := out.Entries[i]
		if aws.ToString(e.ErrorCode) != "InvalidArgument" || aws.ToString(e.ErrorMessage) != msg {
			t.Fatalf("entry %d = %q/%q, want InvalidArgument/%q", i, aws.ToString(e.ErrorCode), aws.ToString(e.ErrorMessage), msg)
		}
	}
}

func requireValidationException(t *testing.T, err error, wantMsg string) {
	t.Helper()

	if err == nil {
		t.Fatal("call succeeded, want ValidationException")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy.APIError: %v", err)
	}

	if apiErr.ErrorCode() != "ValidationException" || apiErr.ErrorMessage() != wantMsg {
		t.Fatalf("error = %s: %q, want ValidationException: %q", apiErr.ErrorCode(), apiErr.ErrorMessage(), wantMsg)
	}
}

func TestSDKPutEventsOverSizeLimit(t *testing.T) {
	client := newEventBridgeClient(t)

	detail := `{"p":"` + strings.Repeat("x", 1<<20) + `"}`

	_, err := client.PutEvents(context.Background(), &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("s"), DetailType: aws.String("t"), Detail: aws.String(detail)},
	}})

	requireValidationException(t, err, "Total size of the entries in the request is over the limit.")
}

func TestSDKPutTargetsConflictingInputs(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("r"), EventPattern: aws.String(`{"source":["s"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	_, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"),
		Targets: []ebtypes.Target{{
			Id:        aws.String("t1"),
			Arn:       aws.String("arn:aws:sqs:us-east-1:123456789012:q"),
			Input:     aws.String("{}"),
			InputPath: aws.String("$.detail"),
		}},
	})

	requireValidationException(t, err, "Only one of Input, InputPath, or InputTransformer must be provided for target t1.")

	list, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{Rule: aws.String("r")})
	if err != nil {
		t.Fatalf("ListTargetsByRule: %v", err)
	}

	if len(list.Targets) != 0 {
		t.Fatalf("targets stored after a rejected PutTargets: %+v", list.Targets)
	}
}
