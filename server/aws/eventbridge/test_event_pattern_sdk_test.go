package eventbridge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

const testEventPatternSample = `{"id":"1","account":"123456789012","time":"2016-01-10T01:29:23Z",` +
	`"region":"us-east-1","resources":[],"source":"com.myapp","detail-type":"t",` +
	`"detail":{"state":"running"}}`

// TestSDKEventBridgeTestEventPattern verifies the TestEventPattern operation:
// a matching event returns Result=true, a non-matching event returns false.
func TestSDKEventBridgeTestEventPattern(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	match, err := client.TestEventPattern(ctx, &awseb.TestEventPatternInput{
		EventPattern: aws.String(`{"source":["com.myapp"],"detail":{"state":["running"]}}`),
		Event:        aws.String(testEventPatternSample),
	})
	if err != nil {
		t.Fatalf("TestEventPattern(match): %v", err)
	}

	if !match.Result {
		t.Fatal("TestEventPattern match = false, want true")
	}

	noMatch, err := client.TestEventPattern(ctx, &awseb.TestEventPatternInput{
		EventPattern: aws.String(`{"source":["com.myapp"],"detail":{"state":["stopped"]}}`),
		Event:        aws.String(testEventPatternSample),
	})
	if err != nil {
		t.Fatalf("TestEventPattern(no-match): %v", err)
	}

	if noMatch.Result {
		t.Fatal("TestEventPattern no-match = true, want false")
	}
}

// TestSDKEventBridgeTestEventPatternInvalid verifies TestEventPattern rejects a
// structurally invalid pattern with InvalidEventPatternException, consistent with
// PutRule.
func TestSDKEventBridgeTestEventPatternInvalid(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	_, err := client.TestEventPattern(ctx, &awseb.TestEventPatternInput{
		EventPattern: aws.String(`{"source":"not-an-array"}`),
		Event:        aws.String(testEventPatternSample),
	})
	if err == nil {
		t.Fatal("TestEventPattern with invalid pattern should fail, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidEventPatternException" {
		t.Fatalf("want InvalidEventPatternException, got %T: %v", err, err)
	}
}

// TestSDKEventBridgeTestEventPatternConsistentWithDelivery verifies TestEventPattern's
// verdict matches actual PutEvents delivery for the same pattern and event.
func TestSDKEventBridgeTestEventPatternConsistentWithDelivery(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-consistency")
	pattern := `{"source":["com.myapp"],"detail":{"state":["running"]}}`

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(pattern),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	res, err := eb.TestEventPattern(ctx, &awseb.TestEventPatternInput{
		EventPattern: aws.String(pattern),
		Event:        aws.String(testEventPatternSample),
	})
	if err != nil {
		t.Fatalf("TestEventPattern: %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("com.myapp"), DetailType: aws.String("t"), Detail: aws.String(`{"state":"running"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	_, count := receiveOne(t, sqs, url)
	delivered := count == 1

	if res.Result != delivered {
		t.Fatalf("TestEventPattern Result=%v but delivery happened=%v — inconsistent", res.Result, delivered)
	}

	if !res.Result {
		t.Fatal("expected both match and delivery to be true")
	}
}
