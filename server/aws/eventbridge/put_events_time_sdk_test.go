package eventbridge_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// TestSDKEventBridgePutEventsEntryTimeHonored verifies that a caller-supplied
// PutEvents entry Time is threaded through to delivery: the delivered
// envelope's "time" field must carry the backdated timestamp the caller
// supplied, not the time of the PutEvents call. Before the fix, the wire
// handler dropped the entry's Time entirely, so every delivered event carried
// the call's own timestamp regardless of what the caller set.
func TestSDKEventBridgePutEventsEntryTimeHonored(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-entry-time")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	// A timestamp far in the past — nothing close to "now" — so honoring it
	// is unambiguous even if the server clock drifts.
	backdated := time.Date(2019, 6, 15, 8, 30, 0, 0, time.UTC)

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{
			Source:     aws.String("app"),
			DetailType: aws.String("t"),
			Detail:     aws.String(`{}`),
			Time:       aws.Time(backdated),
		},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	body, count := receiveOne(t, sqs, url)
	if count != 1 {
		t.Fatalf("delivered %d messages, want 1", count)
	}

	var env struct {
		Time string `json:"time"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope not JSON: %v (%s)", err, body)
	}

	gotTime, err := time.Parse(time.RFC3339, env.Time)
	if err != nil {
		t.Fatalf("envelope time %q not RFC3339: %v", env.Time, err)
	}

	if !gotTime.Equal(backdated) {
		t.Fatalf("delivered envelope time = %s, want the caller-supplied %s (not PutEvents call time)",
			gotTime, backdated)
	}
}

// TestSDKEventBridgePutEventsOmittedTimeDefaultsToNow verifies the converse:
// omitting Time still defaults to the PutEvents call's own time (never the
// zero time), so the fix didn't regress the no-Time case.
func TestSDKEventBridgePutEventsOmittedTimeDefaultsToNow(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	url, arn := makeQueue(t, sqs, "eb-no-entry-time")

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("r"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	before := time.Now().Add(-time.Minute)

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	after := time.Now().Add(time.Minute)

	body, count := receiveOne(t, sqs, url)
	if count != 1 {
		t.Fatalf("delivered %d messages, want 1", count)
	}

	var env struct {
		Time string `json:"time"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope not JSON: %v (%s)", err, body)
	}

	gotTime, err := time.Parse(time.RFC3339, env.Time)
	if err != nil {
		t.Fatalf("envelope time %q not RFC3339: %v", env.Time, err)
	}

	if gotTime.Before(before) || gotTime.After(after) {
		t.Fatalf("envelope time = %s, want within [%s, %s] (the PutEvents call time)", gotTime, before, after)
	}
}
