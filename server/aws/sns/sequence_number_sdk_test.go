package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
)

// TestSDKPublishFifoReturnsSequenceNumber asserts the wire layer surfaces the
// FIFO-only SequenceNumber real SNS returns from Publish, and omits it for a
// standard topic — a real aws-sdk-go-v2 client that orders FIFO messages by
// SequenceNumber otherwise reads an empty value.
func TestSDKPublishFifoReturnsSequenceNumber(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	fifo, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("seq.fifo"),
		Attributes: map[string]string{
			"FifoTopic":                 "true",
			"ContentBasedDeduplication": "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateTopic(fifo): %v", err)
	}

	first, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn:       fifo.TopicArn,
		Message:        aws.String("m1"),
		MessageGroupId: aws.String("g1"),
	})
	if err != nil {
		t.Fatalf("Publish(fifo): %v", err)
	}

	if first.SequenceNumber == nil || *first.SequenceNumber == "" {
		t.Fatal("FIFO Publish returned no SequenceNumber, want a non-empty value")
	}

	second, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn:       fifo.TopicArn,
		Message:        aws.String("m2"),
		MessageGroupId: aws.String("g1"),
	})
	if err != nil {
		t.Fatalf("Publish(fifo) second: %v", err)
	}

	if *second.SequenceNumber <= *first.SequenceNumber {
		t.Fatalf("SequenceNumber not increasing: %q then %q", *first.SequenceNumber, *second.SequenceNumber)
	}

	std, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("seq-standard")})
	if err != nil {
		t.Fatalf("CreateTopic(standard): %v", err)
	}

	out, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn: std.TopicArn,
		Message:  aws.String("hi"),
	})
	if err != nil {
		t.Fatalf("Publish(standard): %v", err)
	}

	if out.SequenceNumber != nil && *out.SequenceNumber != "" {
		t.Fatalf("standard Publish returned SequenceNumber %q, want none", *out.SequenceNumber)
	}
}

// TestSDKPublishBatchFifoReturnsSequenceNumber asserts each successful
// PublishBatch entry on a FIFO topic carries its own SequenceNumber, mirroring
// the PublishBatchResultEntry shape real SNS returns.
func TestSDKPublishBatchFifoReturnsSequenceNumber(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	fifo, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("seqbatch.fifo"),
		Attributes: map[string]string{
			"FifoTopic":                 "true",
			"ContentBasedDeduplication": "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateTopic(fifo): %v", err)
	}

	out, err := client.PublishBatch(ctx, &awssns.PublishBatchInput{
		TopicArn: fifo.TopicArn,
		PublishBatchRequestEntries: []snstypes.PublishBatchRequestEntry{
			{Id: aws.String("a"), Message: aws.String("m1"), MessageGroupId: aws.String("g1")},
			{Id: aws.String("b"), Message: aws.String("m2"), MessageGroupId: aws.String("g2")},
		},
	})
	if err != nil {
		t.Fatalf("PublishBatch(fifo): %v", err)
	}

	if len(out.Failed) != 0 {
		t.Fatalf("PublishBatch had %d failed entries, want 0", len(out.Failed))
	}

	if len(out.Successful) != 2 {
		t.Fatalf("PublishBatch returned %d successful entries, want 2", len(out.Successful))
	}

	for _, e := range out.Successful {
		if e.SequenceNumber == nil || *e.SequenceNumber == "" {
			t.Fatalf("batch entry %q returned no SequenceNumber", aws.ToString(e.Id))
		}
	}
}
