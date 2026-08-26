package cloudwatchlogs_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// TestSDKFilterLogEventsAscendingDeterministic reproduces the multi-stream
// ordering bug: matches across three streams (ts 1000/3000/2000/4000) must come
// back interleaved ascending [1000 2000 3000 4000] on every run.
func TestSDKFilterLogEventsAscendingDeterministic(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/fla/g")}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)
	puts := []struct {
		stream string
		off    time.Duration
	}{
		{"s1", 1000 * time.Millisecond},
		{"s3", 3000 * time.Millisecond},
		{"s2", 2000 * time.Millisecond},
		{"s1", 4000 * time.Millisecond},
	}

	for _, p := range puts {
		_, _ = client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
			LogGroupName: aws.String("/fla/g"), LogStreamName: aws.String(p.stream),
		})
		if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName: aws.String("/fla/g"), LogStreamName: aws.String(p.stream),
			LogEvents: []cwltypes.InputLogEvent{
				{Timestamp: aws.Int64(base.Add(p.off).UnixMilli()), Message: aws.String("m")},
			},
		}); err != nil {
			t.Fatalf("PutLogEvents %s: %v", p.stream, err)
		}
	}

	want := []int64{
		base.Add(1000 * time.Millisecond).UnixMilli(),
		base.Add(2000 * time.Millisecond).UnixMilli(),
		base.Add(3000 * time.Millisecond).UnixMilli(),
		base.Add(4000 * time.Millisecond).UnixMilli(),
	}

	for run := 0; run < 8; run++ {
		out, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
			LogGroupName: aws.String("/fla/g"),
		})
		if err != nil {
			t.Fatalf("FilterLogEvents run %d: %v", run, err)
		}

		if len(out.Events) != 4 {
			t.Fatalf("run %d: got %d events, want 4", run, len(out.Events))
		}

		for i := range out.Events {
			if aws.ToInt64(out.Events[i].Timestamp) != want[i] {
				t.Fatalf("run %d: event[%d] ts = %d, want %d (order = %v)",
					run, i, aws.ToInt64(out.Events[i].Timestamp), want[i], filteredTimestamps(out.Events))
			}
		}
	}
}

func filteredTimestamps(events []cwltypes.FilteredLogEvent) []int64 {
	ms := make([]int64, len(events))
	for i := range events {
		ms[i] = aws.ToInt64(events[i].Timestamp)
	}

	return ms
}
