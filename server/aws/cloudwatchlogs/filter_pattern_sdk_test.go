package cloudwatchlogs_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func TestSDKFilterLogEventsPatternLanguage(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := aws.String("/fp/g"), aws.String("s")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	now := time.Now()
	msgs := []string{
		"[ERROR 419] MISSING ARGUMENTS",
		"[ERROR 400] BAD REQUEST",
		`{"level":"ERROR","latency":50,"user":null}`,
		`{"level":"INFO","latency":5}`,
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
		"ERROR":                                     3,
		"ERROR -ARGUMENTS":                          2,
		"?BAD ?MISSING":                             2,
		`"BAD REQUEST"`:                             1,
		"%ERR.R%":                                   3,
		`{ $.level = "ERROR" }`:                     1,
		`{ $.latency > 10 || $.level = "INFO" }`:    2,
		`{ $.user IS NULL && $.level = ERR* }`:      1,
		`{ $.missing NOT EXISTS && $.latency < 9 }`: 1,
	} {
		out, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: g, FilterPattern: aws.String(pattern)})
		if err != nil {
			t.Fatalf("FilterLogEvents(%q): %v", pattern, err)
		}

		if len(out.Events) != want {
			t.Errorf("FilterLogEvents(%q) = %d events, want %d", pattern, len(out.Events), want)
		}
	}

	var invalid *cwltypes.InvalidParameterException

	for _, bad := range []string{`{ $.a IS NOT NULL }`, `{ $.a EXISTS }`, `{ $.a = `} {
		_, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: g, FilterPattern: aws.String(bad)})
		if !errors.As(err, &invalid) {
			t.Errorf("FilterLogEvents(%q): got %v, want InvalidParameterException", bad, err)
		}
	}
}

func TestSDKTestMetricFilter(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()

	out, err := c.TestMetricFilter(ctx, &cwl.TestMetricFilterInput{
		FilterPattern:    aws.String(`{ $.latency > 10 }`),
		LogEventMessages: []string{`{"latency":5}`, `{"latency":50}`, "plain text"},
	})
	if err != nil {
		t.Fatalf("TestMetricFilter: %v", err)
	}

	if len(out.Matches) != 1 || out.Matches[0].EventNumber != 1 || out.Matches[0].ExtractedValues["$.latency"] != "50" {
		t.Fatalf("matches = %+v", out.Matches)
	}

	var invalid *cwltypes.InvalidParameterException

	if _, err := c.TestMetricFilter(ctx, &cwl.TestMetricFilterInput{
		FilterPattern: aws.String(`{ $.a IS NOT NULL }`), LogEventMessages: []string{"x"},
	}); !errors.As(err, &invalid) {
		t.Fatalf("bad pattern: got %v, want InvalidParameterException", err)
	}
}

func TestSDKPutFilterPatternValidationAndQuota(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g := aws.String("/fp/quota")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	mf := func(name, pattern string) error {
		_, err := c.PutMetricFilter(ctx, &cwl.PutMetricFilterInput{
			LogGroupName: g, FilterName: aws.String(name), FilterPattern: aws.String(pattern),
			MetricTransformations: []cwltypes.MetricTransformation{{
				MetricName: aws.String("M"), MetricNamespace: aws.String("N"), MetricValue: aws.String("1"),
			}},
		})

		return err
	}

	var invalid *cwltypes.InvalidParameterException
	if err := mf("bad", "{ $.a EXISTS }"); !errors.As(err, &invalid) {
		t.Fatalf("bad metric filter pattern: got %v, want InvalidParameterException", err)
	}

	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{
		LogGroupName: g, FilterName: aws.String("bad"), FilterPattern: aws.String("%(x)%"),
		DestinationArn: aws.String("arn:aws:lambda:us-east-1:000000000000:function:f"),
	}); !errors.As(err, &invalid) {
		t.Fatalf("bad subscription filter pattern: got %v, want InvalidParameterException", err)
	}

	for i := range 5 {
		if err := mf(fmt.Sprintf("re%d", i), "%ERR%"); err != nil {
			t.Fatalf("regex filter %d: %v", i, err)
		}
	}

	var limit *cwltypes.LimitExceededException
	if err := mf("re5", "%WARN%"); !errors.As(err, &limit) {
		t.Fatalf("sixth regex filter: got %v, want LimitExceededException", err)
	}
}
