package cloudwatchlogs_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// TestSDKDescribeLogStreamsOrderByLastEventTime reproduces the orderBy bug:
// stream "a" gets the newest event, so DescribeLogStreams with
// orderBy=LastEventTime + descending must return it first (name order would put
// "a" first anyway, so "a" is deliberately given the newest event while "b"
// sorts first by name — descending LastEventTime must still surface "a").
func TestSDKDescribeLogStreamsOrderByLastEventTime(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/ord/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)

	// "b" is name-first but has the older last event; "a" has the newest.
	streamsToEvents := []struct {
		stream string
		off    time.Duration
	}{
		{"b", 1 * time.Second},
		{"a", 10 * time.Second},
	}

	for _, se := range streamsToEvents {
		if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
			LogGroupName: aws.String("/ord/g"), LogStreamName: aws.String(se.stream),
		}); err != nil {
			t.Fatalf("CreateLogStream %s: %v", se.stream, err)
		}
		if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName: aws.String("/ord/g"), LogStreamName: aws.String(se.stream),
			LogEvents: []cwltypes.InputLogEvent{
				{Timestamp: aws.Int64(base.Add(se.off).UnixMilli()), Message: aws.String("m")},
			},
		}); err != nil {
			t.Fatalf("PutLogEvents %s: %v", se.stream, err)
		}
	}

	out, err := client.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName: aws.String("/ord/g"),
		OrderBy:      cwltypes.OrderByLastEventTime,
		Descending:   aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeLogStreams: %v", err)
	}

	if len(out.LogStreams) != 2 {
		t.Fatalf("got %d streams, want 2", len(out.LogStreams))
	}

	if aws.ToString(out.LogStreams[0].LogStreamName) != "a" {
		t.Fatalf("orderBy=LastEventTime descending first = %q, want a (newest event)",
			aws.ToString(out.LogStreams[0].LogStreamName))
	}
}
