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

func TestSDKSpaceDelimitedPatterns(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := aws.String("/sd/g"), aws.String("s")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	now := time.Now()
	msgs := []string{
		`127.0.0.1 - frank [10/Oct/2000:13:25:15 -0700] "GET /index.html HTTP/1.0" 200 1534`,
		`127.0.0.1 - frank [10/Oct/2000:13:35:22 -0700] "GET /about.html HTTP/1.0" 404 2000`,
		"ERROR 09/25/2014 12:00:02 Failed to process request",
	}

	events := make([]cwltypes.InputLogEvent, 0, len(msgs))
	for i, m := range msgs {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(now.Add(time.Duration(i) * time.Millisecond).UnixMilli()), Message: aws.String(m),
		})
	}

	if _, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: events}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	for pattern, want := range map[string]int{
		`[..., request=*.html*, status_code=4*, bytes]`:          1,
		`[ip, id, user, ts, request, status_code, bytes > 1000]`: 2,
		`[w1=ERROR, w2]`: 1,
		`[w1=ERROR]`:     0,
		`[]`:             3,
	} {
		out, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: g, FilterPattern: aws.String(pattern)})
		if err != nil {
			t.Fatalf("FilterLogEvents(%q): %v", pattern, err)
		}

		if len(out.Events) != want {
			t.Errorf("FilterLogEvents(%q) = %d events, want %d", pattern, len(out.Events), want)
		}
	}

	tm, err := c.TestMetricFilter(ctx, &cwl.TestMetricFilterInput{
		FilterPattern: aws.String(`[..., status_code=404, size]`), LogEventMessages: msgs,
	})
	if err != nil {
		t.Fatalf("TestMetricFilter: %v", err)
	}

	if len(tm.Matches) != 1 || tm.Matches[0].EventNumber != 1 || tm.Matches[0].ExtractedValues["$size"] != "2000" ||
		tm.Matches[0].ExtractedValues["$4"] != "10/Oct/2000:13:35:22 -0700" {
		t.Fatalf("TestMetricFilter matches = %+v", tm.Matches)
	}

	if _, err := c.PutMetricFilter(ctx, &cwl.PutMetricFilterInput{
		LogGroupName: g, FilterName: aws.String("bytes"), FilterPattern: aws.String(`[..., status_code=404, bytes]`),
		MetricTransformations: []cwltypes.MetricTransformation{{
			MetricName: aws.String("Bytes"), MetricNamespace: aws.String("Web"), MetricValue: aws.String("$bytes"),
		}},
	}); err != nil {
		t.Fatalf("PutMetricFilter: %v", err)
	}

	var invalid *cwltypes.InvalidParameterException

	if _, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName: g, FilterPattern: aws.String(`[a, b=1 || c=2]`),
	}); !errors.As(err, &invalid) {
		t.Fatalf("condition on another field: got %v, want InvalidParameterException", err)
	}
}
