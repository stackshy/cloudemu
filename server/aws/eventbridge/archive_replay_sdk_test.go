package eventbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

const sdkBusARN = "arn:aws:events:us-east-1:123456789012:event-bus/"

func requireAPIError(t *testing.T, err error, code, msg string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want %s", err, code)
	}

	if apiErr.ErrorCode() != code || (msg != "" && apiErr.ErrorMessage() != msg) {
		t.Fatalf("error = %s: %q, want %s: %q", apiErr.ErrorCode(), apiErr.ErrorMessage(), code, msg)
	}
}

// TestSDKArchiveAndReplayLifecycle drives archives and replays through the real
// SDK: events put on a bus are archived, a replay re-delivers them to the one
// rule it names with a replay-name field, and the replay completes.
func TestSDKArchiveAndReplayLifecycle(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Minute)

	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("b1")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	created, err := eb.CreateArchive(ctx, &awseb.CreateArchiveInput{
		ArchiveName: aws.String("arch"), EventSourceArn: aws.String(sdkBusARN + "b1"),
		EventPattern: aws.String(`{"source":["app"]}`), RetentionDays: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}

	if created.State != ebtypes.ArchiveStateEnabled || aws.ToString(created.ArchiveArn) == "" {
		t.Fatalf("CreateArchive = %+v", created)
	}

	url, arn := makeQueue(t, sqs, "replay-q")

	rule, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("r"), EventBusName: aws.String("b1"), EventPattern: aws.String(`{"source":["app"]}`),
	})
	if err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"), EventBusName: aws.String("b1"),
		Targets: []ebtypes.Target{{Id: aws.String("q"), Arn: aws.String(arn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String("{}"), EventBusName: aws.String("b1")},
		{Source: aws.String("other"), DetailType: aws.String("t"), Detail: aws.String("{}"), EventBusName: aws.String("b1")},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	if _, n := receiveOne(t, sqs, url); n != 1 {
		t.Fatalf("original delivery count = %d, want 1", n)
	}

	desc, err := eb.DescribeArchive(ctx, &awseb.DescribeArchiveInput{ArchiveName: aws.String("arch")})
	if err != nil {
		t.Fatalf("DescribeArchive: %v", err)
	}

	if desc.EventCount != 1 || desc.SizeBytes == 0 || aws.ToString(desc.EventPattern) != `{"source":["app"]}` {
		t.Fatalf("DescribeArchive = count %d size %d pattern %q", desc.EventCount, desc.SizeBytes, aws.ToString(desc.EventPattern))
	}

	started, err := eb.StartReplay(ctx, &awseb.StartReplayInput{
		ReplayName: aws.String("rp"), EventSourceArn: created.ArchiveArn,
		EventStartTime: aws.Time(start), EventEndTime: aws.Time(time.Now().Add(time.Minute)),
		Destination: &ebtypes.ReplayDestination{Arn: aws.String(sdkBusARN + "b1"), FilterArns: []string{aws.ToString(rule.RuleArn)}},
	})
	if err != nil {
		t.Fatalf("StartReplay: %v", err)
	}

	if started.State != ebtypes.ReplayStateStarting {
		t.Fatalf("StartReplay state = %s, want STARTING", started.State)
	}

	body, n := receiveOne(t, sqs, url)
	if n != 1 {
		t.Fatalf("replayed delivery count = %d, want 1", n)
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(body), &env); err != nil || env["replay-name"] != "rp" {
		t.Fatalf("replayed body = %s, want replay-name rp", body)
	}

	rp, err := eb.DescribeReplay(ctx, &awseb.DescribeReplayInput{ReplayName: aws.String("rp")})
	if err != nil {
		t.Fatalf("DescribeReplay: %v", err)
	}

	if rp.State != ebtypes.ReplayStateCompleted || rp.ReplayEndTime == nil || len(rp.Destination.FilterArns) != 1 {
		t.Fatalf("DescribeReplay = %+v", rp)
	}

	_, err = eb.CancelReplay(ctx, &awseb.CancelReplayInput{ReplayName: aws.String("rp")})

	var illegal *ebtypes.IllegalStatusException
	if !errors.As(err, &illegal) {
		t.Fatalf("CancelReplay on a completed replay = %v, want IllegalStatusException", err)
	}

	replays, err := eb.ListReplays(ctx, &awseb.ListReplaysInput{NamePrefix: aws.String("r")})
	if err != nil || len(replays.Replays) != 1 {
		t.Fatalf("ListReplays = %+v, %v", replays, err)
	}

	archives, err := eb.ListArchives(ctx, &awseb.ListArchivesInput{EventSourceArn: aws.String(sdkBusARN + "b1")})
	if err != nil || len(archives.Archives) != 1 || archives.Archives[0].EventCount != 1 {
		t.Fatalf("ListArchives = %+v, %v", archives, err)
	}

	if _, err := eb.UpdateArchive(ctx, &awseb.UpdateArchiveInput{ArchiveName: aws.String("arch"), RetentionDays: aws.Int32(0)}); err != nil {
		t.Fatalf("UpdateArchive: %v", err)
	}

	if _, err := eb.DeleteArchive(ctx, &awseb.DeleteArchiveInput{ArchiveName: aws.String("arch")}); err != nil {
		t.Fatalf("DeleteArchive: %v", err)
	}

	_, err = eb.DescribeArchive(ctx, &awseb.DescribeArchiveInput{ArchiveName: aws.String("arch")})

	var notFound *ebtypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeArchive after delete = %v, want ResourceNotFoundException", err)
	}
}

func TestSDKArchiveErrors(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	_, err := client.CreateArchive(ctx, &awseb.CreateArchiveInput{
		ArchiveName: aws.String("a"), EventSourceArn: aws.String(sdkBusARN + "ghost"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", "Event bus ghost does not exist.")

	_, err = client.CreateArchive(ctx, &awseb.CreateArchiveInput{
		ArchiveName: aws.String("a"), EventSourceArn: aws.String(sdkBusARN + "default"), EventPattern: aws.String(`{"source":"x"}`),
	})

	var badPattern *ebtypes.InvalidEventPatternException
	if !errors.As(err, &badPattern) {
		t.Fatalf("CreateArchive with a bad pattern = %v, want InvalidEventPatternException", err)
	}

	_, err = client.UpdateArchive(ctx, &awseb.UpdateArchiveInput{ArchiveName: aws.String("ghost")})
	requireAPIError(t, err, "ValidationException", "At least one of EventPattern, RetentionDays or Description must be provided.")

	_, err = client.ListArchives(ctx, &awseb.ListArchivesInput{NamePrefix: aws.String("a"), State: ebtypes.ArchiveStateEnabled})
	requireAPIError(t, err, "ValidationException",
		"At most one filter is allowed for ListArchives. Use either : State, EventSourceArn, or NamePrefix.")

	_, err = client.StartReplay(ctx, &awseb.StartReplayInput{
		ReplayName: aws.String("rp"), EventSourceArn: aws.String("arn:aws:events:us-east-1:123456789012:archive/nope"),
		EventStartTime: aws.Time(time.Now().Add(-time.Hour)), EventEndTime: aws.Time(time.Now()),
		Destination: &ebtypes.ReplayDestination{Arn: aws.String(sdkBusARN + "default")},
	})
	requireAPIError(t, err, "ValidationException", "Parameter EventSourceArn is not valid. Reason: Archive nope does not exist.")

	_, err = client.DescribeReplay(ctx, &awseb.DescribeReplayInput{ReplayName: aws.String("nope")})
	requireAPIError(t, err, "ResourceNotFoundException", "Replay nope does not exist.")
}
