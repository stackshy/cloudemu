package cloudwatchlogs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// TestSDKGetLogEventsTimestampOrder reproduces the out-of-order ingestion bug:
// a batch stamped later is put before an earlier one, yet GetLogEvents
// (startFromHead=true) must return them in timestamp order [early, late].
func TestSDKGetLogEventsTimestampOrder(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	mustLogGroupStream(t, client, "/order/g", "s1")

	base := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/order/g"), LogStreamName: aws.String("s1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(base.Add(5 * time.Second).UnixMilli()), Message: aws.String("late")},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents late: %v", err)
	}

	if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/order/g"), LogStreamName: aws.String("s1"),
		LogEvents: []cwltypes.InputLogEvent{
			{Timestamp: aws.Int64(base.Add(1 * time.Second).UnixMilli()), Message: aws.String("early")},
		},
	}); err != nil {
		t.Fatalf("PutLogEvents early: %v", err)
	}

	out, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/order/g"), LogStreamName: aws.String("s1"),
		StartFromHead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("GetLogEvents: %v", err)
	}

	if len(out.Events) != 2 ||
		aws.ToString(out.Events[0].Message) != "early" ||
		aws.ToString(out.Events[1].Message) != "late" {
		t.Fatalf("GetLogEvents = %+v, want [early late] in timestamp order", out.Events)
	}
}

// TestSDKRetentionUnsetAndPut reproduces the retention default bug: a group made
// without a retention policy reports retentionInDays as nil (never expire), and
// PutRetentionPolicy(7) is then reflected.
func TestSDKRetentionUnsetAndPut(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/ret/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	desc, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("/ret/g"),
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}

	if len(desc.LogGroups) != 1 {
		t.Fatalf("got %d groups, want 1", len(desc.LogGroups))
	}

	if desc.LogGroups[0].RetentionInDays != nil {
		t.Fatalf("RetentionInDays = %d, want nil (never expire) for a group with no policy",
			aws.ToInt32(desc.LogGroups[0].RetentionInDays))
	}

	if _, err := client.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{
		LogGroupName: aws.String("/ret/g"), RetentionInDays: aws.Int32(7),
	}); err != nil {
		t.Fatalf("PutRetentionPolicy: %v", err)
	}

	after, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String("/ret/g"),
	})
	if err != nil {
		t.Fatalf("DescribeLogGroups after put: %v", err)
	}

	if aws.ToInt32(after.LogGroups[0].RetentionInDays) != 7 {
		t.Fatalf("RetentionInDays = %d, want 7", aws.ToInt32(after.LogGroups[0].RetentionInDays))
	}
}

// TestSDKGetLogEventsMissingStream reproduces the missing-stream bug:
// GetLogEvents on a stream that does not exist must return
// ResourceNotFoundException, not an empty page.
func TestSDKGetLogEventsMissingStream(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/miss/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	_, err := client.GetLogEvents(ctx, &cwl.GetLogEventsInput{
		LogGroupName: aws.String("/miss/g"), LogStreamName: aws.String("nope"),
	})

	var notFound *cwltypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("GetLogEvents(missing stream): got %v, want ResourceNotFoundException", err)
	}
}
