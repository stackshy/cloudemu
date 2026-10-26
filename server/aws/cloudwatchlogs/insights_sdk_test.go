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

func resultValue(row []cwltypes.ResultField, name string) string {
	for _, f := range row {
		if aws.ToString(f.Field) == name {
			return aws.ToString(f.Value)
		}
	}

	return ""
}

func TestSDKLogsInsights(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := aws.String("/insights/g"), aws.String("s")

	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: g}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: g, LogStreamName: s}); err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}

	now := time.Now()
	msgs := []string{`{"level":"ERROR","latency":120}`, `{"level":"INFO","latency":15}`, "plain ERROR Exception"}

	events := make([]cwltypes.InputLogEvent, 0, len(msgs))
	for i, m := range msgs {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(now.Add(time.Duration(i) * time.Millisecond).UnixMilli()), Message: aws.String(m),
		})
	}

	if _, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: g, LogStreamName: s, LogEvents: events}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	start, err := c.StartQuery(ctx, &cwl.StartQueryInput{
		LogGroupName: g, StartTime: aws.Int64(now.Add(-time.Hour).Unix()), EndTime: aws.Int64(now.Unix() + 1),
		QueryString: aws.String("fields @timestamp, level, latency | filter latency > 10 | sort latency desc"),
	})
	if err != nil {
		t.Fatalf("StartQuery: %v", err)
	}

	// Poll the way real clients do: until the query leaves Running.
	var res *cwl.GetQueryResultsOutput
	for range 5 {
		res, err = c.GetQueryResults(ctx, &cwl.GetQueryResultsInput{QueryId: start.QueryId})
		if err != nil {
			t.Fatalf("GetQueryResults: %v", err)
		}

		if res.Status != cwltypes.QueryStatusRunning && res.Status != cwltypes.QueryStatusScheduled {
			break
		}
	}

	if res.Status != cwltypes.QueryStatusComplete || len(res.Results) != 2 {
		t.Fatalf("results = %s, %d rows", res.Status, len(res.Results))
	}

	if resultValue(res.Results[0], "latency") != "120" || resultValue(res.Results[0], "@ptr") == "" {
		t.Fatalf("first row = %+v", res.Results[0])
	}

	if res.Statistics.RecordsMatched != 2 || res.Statistics.RecordsScanned != 3 || res.Statistics.BytesScanned <= 0 {
		t.Fatalf("statistics = %+v", res.Statistics)
	}

	rec, err := c.GetLogRecord(ctx, &cwl.GetLogRecordInput{LogRecordPointer: aws.String(resultValue(res.Results[0], "@ptr"))})
	if err != nil || rec.LogRecord["@message"] != msgs[0] || rec.LogRecord["level"] != "ERROR" {
		t.Fatalf("GetLogRecord = %+v, %v", rec, err)
	}

	desc, err := c.DescribeQueries(ctx, &cwl.DescribeQueriesInput{Status: cwltypes.QueryStatusComplete, LogGroupName: g})
	if err != nil || len(desc.Queries) != 1 || aws.ToString(desc.Queries[0].QueryId) != aws.ToString(start.QueryId) {
		t.Fatalf("DescribeQueries = %+v, %v", desc, err)
	}

	var invalid *cwltypes.InvalidParameterException
	if _, err := c.StopQuery(ctx, &cwl.StopQueryInput{QueryId: start.QueryId}); !errors.As(err, &invalid) {
		t.Fatalf("StopQuery on a finished query: got %v, want InvalidParameterException", err)
	}

	fresh, err := c.StartQuery(ctx, &cwl.StartQueryInput{
		LogGroupName: g, StartTime: aws.Int64(now.Add(-time.Hour).Unix()), EndTime: aws.Int64(now.Unix() + 1),
		QueryString: aws.String("fields @message"),
	})
	if err != nil {
		t.Fatalf("StartQuery: %v", err)
	}

	stop, err := c.StopQuery(ctx, &cwl.StopQueryInput{QueryId: fresh.QueryId})
	if err != nil || !stop.Success {
		t.Fatalf("StopQuery = %+v, %v", stop, err)
	}

	cancelled, err := c.GetQueryResults(ctx, &cwl.GetQueryResultsInput{QueryId: fresh.QueryId})
	if err != nil || cancelled.Status != cwltypes.QueryStatusCancelled {
		t.Fatalf("after StopQuery = %+v, %v", cancelled, err)
	}

	fields, err := c.GetLogGroupFields(ctx, &cwl.GetLogGroupFieldsInput{LogGroupName: g, Time: aws.Int64(now.Unix())})
	if err != nil || len(fields.LogGroupFields) == 0 || aws.ToString(fields.LogGroupFields[0].Name) == "" {
		t.Fatalf("GetLogGroupFields = %+v, %v", fields, err)
	}

	var mq *cwltypes.MalformedQueryException
	if _, err := c.StartQuery(ctx, &cwl.StartQueryInput{
		LogGroupName: g, StartTime: aws.Int64(now.Add(-time.Hour).Unix()), EndTime: aws.Int64(now.Unix()),
		QueryString: aws.String("fields @message | filter latency >"),
	}); !errors.As(err, &mq) || mq.QueryCompileError == nil || mq.QueryCompileError.Location == nil {
		t.Fatalf("malformed query: got %v, want MalformedQueryException with a location", err)
	}

	var nf *cwltypes.ResourceNotFoundException
	if _, err := c.StartQuery(ctx, &cwl.StartQueryInput{
		LogGroupName: aws.String("/insights/none"), StartTime: aws.Int64(0), EndTime: aws.Int64(now.Unix()),
		QueryString: aws.String("fields @message"),
	}); !errors.As(err, &nf) {
		t.Fatalf("missing group: got %v, want ResourceNotFoundException", err)
	}
}
