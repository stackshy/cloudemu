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

// runInsights starts a query over the last hour and polls until it leaves
// Running, the way real clients do.
func runInsights(t *testing.T, c *cwl.Client, group, query string) *cwl.GetQueryResultsOutput {
	t.Helper()

	ctx := context.Background()
	now := time.Now()

	start, err := c.StartQuery(ctx, &cwl.StartQueryInput{
		LogGroupName: aws.String(group), StartTime: aws.Int64(now.Add(-time.Hour).Unix()), EndTime: aws.Int64(now.Unix() + 1),
		QueryString: aws.String(query),
	})
	if err != nil {
		t.Fatalf("StartQuery(%q): %v", query, err)
	}

	for range 5 {
		res, rerr := c.GetQueryResults(ctx, &cwl.GetQueryResultsInput{QueryId: start.QueryId})
		if rerr != nil {
			t.Fatalf("GetQueryResults: %v", rerr)
		}

		if res.Status != cwltypes.QueryStatusRunning && res.Status != cwltypes.QueryStatusScheduled {
			return res
		}
	}

	t.Fatalf("query %q never completed", query)

	return nil
}

func TestSDKLogsInsightsStatsParseDedup(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := aws.String("/insights/stats"), aws.String("s")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	// Events sit at the start of the current hour so they share one hourly bin.
	now := time.Now()
	hour := now.UTC().Truncate(time.Hour)
	msgs := []string{
		`{"level":"ERROR","latency":120}`, `{"level":"INFO","latency":15}`, `{"level":"INFO","latency":45}`,
		"user=alice action=login", "user=bob action=logout",
	}

	events := make([]cwltypes.InputLogEvent, 0, len(msgs))
	for i, m := range msgs {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(hour.Add(time.Duration(i) * time.Millisecond).UnixMilli()), Message: aws.String(m),
		})
	}

	if _, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: events}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	res := runInsights(t, c, *g, "filter ispresent(level) | stats count(*) as n, avg(latency) as a by level | sort level asc")
	if len(res.Results) != 2 || resultValue(res.Results[1], "n") != "2" || resultValue(res.Results[1], "a") != "30" ||
		resultValue(res.Results[1], "@ptr") != "" {
		t.Fatalf("stats by level = %+v", res.Results)
	}

	if res.Statistics.RecordsMatched != 3 || res.Statistics.RecordsScanned != 5 {
		t.Fatalf("statistics = %+v, want 3 matched of 5 scanned", res.Statistics)
	}

	res = runInsights(t, c, *g, "stats count(*) as n by bin(1h)")
	if len(res.Results) != 1 || resultValue(res.Results[0], "n") != "5" ||
		resultValue(res.Results[0], "bin(1h)") != hour.Format("2006-01-02 15:04:05.000") {
		t.Fatalf("stats by bin = %+v", res.Results)
	}

	res = runInsights(t, c, *g, `parse @message "user=* action=*" as user, action | filter ispresent(user) | sort user asc | display user, action`)
	if len(res.Results) != 2 || resultValue(res.Results[0], "user") != "alice" || resultValue(res.Results[1], "action") != "logout" {
		t.Fatalf("parse = %+v", res.Results)
	}

	res = runInsights(t, c, *g, "fields level, latency | filter ispresent(level) | dedup level")
	if len(res.Results) != 2 || resultValue(res.Results[0], "latency") != "45" || res.Statistics.RecordsMatched != 3 {
		t.Fatalf("dedup = %+v, %+v", res.Results, res.Statistics)
	}

	_, err := c.StartQuery(ctx, &cwl.StartQueryInput{
		LogGroupName: g, StartTime: aws.Int64(now.Add(-time.Hour).Unix()), EndTime: aws.Int64(now.Unix() + 1),
		QueryString: aws.String("sort @timestamp | stats count(*)"),
	})

	var mq *cwltypes.MalformedQueryException
	if !errors.As(err, &mq) {
		t.Fatalf("sort before stats: got %v, want MalformedQueryException", err)
	}
}
