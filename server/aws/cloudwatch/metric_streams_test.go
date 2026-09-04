package cloudwatch_test

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TestSDKMetricStreamLifecycle drives the aws-sdk-go-v2 client through the
// aws_cloudwatch_metric_stream flow: PutMetricStream, GetMetricStream,
// ListMetricStreams, StopMetricStreams/StartMetricStreams, tagging via
// TagResource/ListTagsForResource/UntagResource, and DeleteMetricStream.
func TestSDKMetricStreamLifecycle(t *testing.T) {
	client, ctx := newCWClient(t)

	firehoseARN := "arn:aws:firehose:us-east-1:123456789098:deliverystream/MyFirehose"
	roleARN := "arn:aws:iam::123456789098:role/MyFirehoseWriteAccessRole"

	put, err := client.PutMetricStream(ctx, &awscw.PutMetricStreamInput{
		Name:         aws.String("my-stream"),
		FirehoseArn:  aws.String(firehoseARN),
		RoleArn:      aws.String(roleARN),
		OutputFormat: cwtypes.MetricStreamOutputFormatJson,
		IncludeFilters: []cwtypes.MetricStreamFilter{
			{Namespace: aws.String("AWS/EC2")},
		},
		Tags: []cwtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("PutMetricStream: %v", err)
	}
	if aws.ToString(put.Arn) == "" {
		t.Fatal("PutMetricStream: empty Arn")
	}

	got, err := client.GetMetricStream(ctx, &awscw.GetMetricStreamInput{Name: aws.String("my-stream")})
	if err != nil {
		t.Fatalf("GetMetricStream: %v", err)
	}
	if aws.ToString(got.Name) != "my-stream" {
		t.Fatalf("Name = %q, want my-stream", aws.ToString(got.Name))
	}
	if aws.ToString(got.FirehoseArn) != firehoseARN {
		t.Fatalf("FirehoseArn = %q, want %q", aws.ToString(got.FirehoseArn), firehoseARN)
	}
	if aws.ToString(got.State) != "running" {
		t.Fatalf("State = %q, want running (newly created streams start running)", aws.ToString(got.State))
	}
	if len(got.IncludeFilters) != 1 || aws.ToString(got.IncludeFilters[0].Namespace) != "AWS/EC2" {
		t.Fatalf("IncludeFilters = %+v, want one AWS/EC2 filter", got.IncludeFilters)
	}

	// A second stream so ListMetricStreams has more than one entry.
	if _, err := client.PutMetricStream(ctx, &awscw.PutMetricStreamInput{
		Name:         aws.String("other-stream"),
		FirehoseArn:  aws.String(firehoseARN),
		RoleArn:      aws.String(roleARN),
		OutputFormat: cwtypes.MetricStreamOutputFormatJson,
	}); err != nil {
		t.Fatalf("PutMetricStream other: %v", err)
	}

	list, err := client.ListMetricStreams(ctx, &awscw.ListMetricStreamsInput{})
	if err != nil {
		t.Fatalf("ListMetricStreams: %v", err)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("Entries = %d, want 2", len(list.Entries))
	}

	// Tag lifecycle: ListTagsForResource, TagResource, UntagResource.
	tags, err := client.ListTagsForResource(ctx, &awscw.ListTagsForResourceInput{ResourceARN: put.Arn})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}
	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" || aws.ToString(tags.Tags[0].Value) != "prod" {
		t.Fatalf("Tags = %+v, want [env=prod]", tags.Tags)
	}

	if _, err := client.TagResource(ctx, &awscw.TagResourceInput{
		ResourceARN: put.Arn,
		Tags:        []cwtypes.Tag{{Key: aws.String("team"), Value: aws.String("sre")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err = client.ListTagsForResource(ctx, &awscw.ListTagsForResourceInput{ResourceARN: put.Arn})
	if err != nil {
		t.Fatalf("ListTagsForResource after tag: %v", err)
	}
	if len(tags.Tags) != 2 {
		t.Fatalf("Tags after TagResource = %d, want 2", len(tags.Tags))
	}

	if _, err := client.UntagResource(ctx, &awscw.UntagResourceInput{
		ResourceARN: put.Arn,
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, err = client.ListTagsForResource(ctx, &awscw.ListTagsForResourceInput{ResourceARN: put.Arn})
	if err != nil {
		t.Fatalf("ListTagsForResource after untag: %v", err)
	}
	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "team" {
		t.Fatalf("Tags after UntagResource = %+v, want [team=sre]", tags.Tags)
	}

	// Stop then start.
	if _, err := client.StopMetricStreams(ctx, &awscw.StopMetricStreamsInput{
		Names: []string{"my-stream"},
	}); err != nil {
		t.Fatalf("StopMetricStreams: %v", err)
	}

	got, err = client.GetMetricStream(ctx, &awscw.GetMetricStreamInput{Name: aws.String("my-stream")})
	if err != nil {
		t.Fatalf("GetMetricStream after stop: %v", err)
	}
	if aws.ToString(got.State) != "stopped" {
		t.Fatalf("State after stop = %q, want stopped", aws.ToString(got.State))
	}

	if _, err := client.StartMetricStreams(ctx, &awscw.StartMetricStreamsInput{
		Names: []string{"my-stream"},
	}); err != nil {
		t.Fatalf("StartMetricStreams: %v", err)
	}

	got, err = client.GetMetricStream(ctx, &awscw.GetMetricStreamInput{Name: aws.String("my-stream")})
	if err != nil {
		t.Fatalf("GetMetricStream after start: %v", err)
	}
	if aws.ToString(got.State) != "running" {
		t.Fatalf("State after start = %q, want running", aws.ToString(got.State))
	}

	// Delete then confirm.
	if _, err := client.DeleteMetricStream(ctx, &awscw.DeleteMetricStreamInput{
		Name: aws.String("my-stream"),
	}); err != nil {
		t.Fatalf("DeleteMetricStream: %v", err)
	}

	if _, err := client.GetMetricStream(ctx, &awscw.GetMetricStreamInput{Name: aws.String("my-stream")}); err == nil {
		t.Fatal("GetMetricStream after delete: expected error, got nil")
	}
}

// TestSDKPutMetricStreamValidation confirms server-side validation of the
// closed OutputFormat enum and the mutually-exclusive IncludeFilters /
// ExcludeFilters combination, matching real PutMetricStream.
func TestSDKPutMetricStreamValidation(t *testing.T) {
	client, ctx := newCWClient(t)

	firehoseARN := "arn:aws:firehose:us-east-1:123456789098:deliverystream/MyFirehose"
	roleARN := "arn:aws:iam::123456789098:role/MyFirehoseWriteAccessRole"

	t.Run("invalid OutputFormat is rejected", func(t *testing.T) {
		_, err := client.PutMetricStream(ctx, &awscw.PutMetricStreamInput{
			Name:         aws.String("bad-format"),
			FirehoseArn:  aws.String(firehoseARN),
			RoleArn:      aws.String(roleARN),
			OutputFormat: cwtypes.MetricStreamOutputFormat("csv"),
		})
		if err == nil {
			t.Fatal("expected error for invalid OutputFormat, got nil")
		}
	})

	t.Run("include and exclude filters together is rejected", func(t *testing.T) {
		_, err := client.PutMetricStream(ctx, &awscw.PutMetricStreamInput{
			Name:           aws.String("both-filters"),
			FirehoseArn:    aws.String(firehoseARN),
			RoleArn:        aws.String(roleARN),
			OutputFormat:   cwtypes.MetricStreamOutputFormatJson,
			IncludeFilters: []cwtypes.MetricStreamFilter{{Namespace: aws.String("AWS/EC2")}},
			ExcludeFilters: []cwtypes.MetricStreamFilter{{Namespace: aws.String("AWS/ELB")}},
		})
		if err == nil {
			t.Fatal("expected error for IncludeFilters+ExcludeFilters, got nil")
		}
	})
}

// TestSDKGetMetricStreamNotFound confirms an unknown metric stream is a
// client error.
func TestSDKGetMetricStreamNotFound(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.GetMetricStream(ctx, &awscw.GetMetricStreamInput{
		Name: aws.String("missing"),
	}); err == nil {
		t.Fatal("GetMetricStream for unknown name: expected error, got nil")
	}
}
