package cloudwatchlogs_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// seedFilterStreams creates a group with three streams (app-1, app-2, db-1),
// each carrying one event whose message is the stream name, so a caller can tell
// which streams a FilterLogEvents call actually searched.
func seedFilterStreams(t *testing.T, client *cwl.Client, group string) {
	t.Helper()

	ctx := context.Background()

	if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)

	for _, s := range []string{"app-1", "app-2", "db-1"} {
		if _, err := client.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
			LogGroupName: aws.String(group), LogStreamName: aws.String(s),
		}); err != nil {
			t.Fatalf("CreateLogStream(%s): %v", s, err)
		}

		if _, err := client.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName: aws.String(group), LogStreamName: aws.String(s),
			LogEvents: []cwltypes.InputLogEvent{{
				Timestamp: aws.Int64(base.UnixMilli()),
				Message:   aws.String(s),
			}},
		}); err != nil {
			t.Fatalf("PutLogEvents(%s): %v", s, err)
		}
	}
}

func filteredStreams(events []cwltypes.FilteredLogEvent) []string {
	names := make([]string, 0, len(events))
	for i := range events {
		names = append(names, aws.ToString(events[i].LogStreamName))
	}

	sort.Strings(names)

	return names
}

// TestSDKFilterLogEventsLogStreamNames guards that logStreamNames restricts the
// result to only the listed streams, including when the list has two or more
// entries (the single-element fast path is not the only supported case).
func TestSDKFilterLogEventsLogStreamNames(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()
	seedFilterStreams(t, client, "/scope/names")

	out, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName:   aws.String("/scope/names"),
		LogStreamNames: []string{"app-1", "app-2"},
	})
	if err != nil {
		t.Fatalf("FilterLogEvents: %v", err)
	}

	got := filteredStreams(out.Events)
	want := []string{"app-1", "app-2"}

	if len(got) != len(want) {
		t.Fatalf("FilterLogEvents streams = %v, want %v (db-1 must be excluded)", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FilterLogEvents streams = %v, want %v", got, want)
		}
	}
}

// TestSDKFilterLogEventsLogStreamNamePrefix guards that logStreamNamePrefix
// restricts the result to streams whose name starts with the prefix.
func TestSDKFilterLogEventsLogStreamNamePrefix(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()
	seedFilterStreams(t, client, "/scope/prefix")

	out, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName:        aws.String("/scope/prefix"),
		LogStreamNamePrefix: aws.String("app-"),
	})
	if err != nil {
		t.Fatalf("FilterLogEvents: %v", err)
	}

	got := filteredStreams(out.Events)
	want := []string{"app-1", "app-2"}

	if len(got) != len(want) {
		t.Fatalf("FilterLogEvents streams = %v, want %v (db-1 must be excluded)", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FilterLogEvents streams = %v, want %v", got, want)
		}
	}
}

// TestSDKFilterLogEventsNamesAndPrefixConflict guards that specifying both
// logStreamNames and logStreamNamePrefix is rejected as InvalidParameterException.
func TestSDKFilterLogEventsNamesAndPrefixConflict(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()
	seedFilterStreams(t, client, "/scope/conflict")

	_, err := client.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{
		LogGroupName:        aws.String("/scope/conflict"),
		LogStreamNames:      []string{"app-1"},
		LogStreamNamePrefix: aws.String("app-"),
	})

	var invalid *cwltypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("FilterLogEvents(names+prefix): got %v, want InvalidParameterException", err)
	}
}
