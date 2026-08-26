package sqs_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// TestSDKListDeadLetterSourceQueuesPagination walks ListDeadLetterSourceQueues
// across pages: three source queues redrive to one DLQ, so MaxResults=2 yields a
// full page with a token then a final page without one, each source URL once.
func TestSDKListDeadLetterSourceQueuesPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	dlq, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dlq")})
	if err != nil {
		t.Fatalf("create dlq: %v", err)
	}

	dlqAttrs, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       dlq.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("get dlq arn: %v", err)
	}

	redrive := fmt.Sprintf(`{"deadLetterTargetArn":%q,"maxReceiveCount":2}`, dlqAttrs.Attributes["QueueArn"])

	for _, name := range []string{"src1", "src2", "src3"} {
		if _, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
			QueueName:  aws.String(name),
			Attributes: map[string]string{"RedrivePolicy": redrive},
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	page1, err := client.ListDeadLetterSourceQueues(ctx, &awssqs.ListDeadLetterSourceQueuesInput{
		QueueUrl: dlq.QueueUrl, MaxResults: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListDeadLetterSourceQueues page1: %v", err)
	}

	if len(page1.QueueUrls) != 2 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d urls token=%q, want 2 with token", len(page1.QueueUrls), aws.ToString(page1.NextToken))
	}

	page2, err := client.ListDeadLetterSourceQueues(ctx, &awssqs.ListDeadLetterSourceQueuesInput{
		QueueUrl: dlq.QueueUrl, MaxResults: aws.Int32(2), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListDeadLetterSourceQueues page2: %v", err)
	}

	if len(page2.QueueUrls) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d urls token=%q, want 1 no token", len(page2.QueueUrls), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, u := range append(page1.QueueUrls, page2.QueueUrls...) {
		if seen[u] {
			t.Fatalf("source url %q returned twice across pages", u)
		}

		seen[u] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique source urls, want 3", len(seen))
	}

	all, err := client.ListDeadLetterSourceQueues(ctx, &awssqs.ListDeadLetterSourceQueuesInput{
		QueueUrl: dlq.QueueUrl,
	})
	if err != nil {
		t.Fatalf("ListDeadLetterSourceQueues all: %v", err)
	}

	if len(all.QueueUrls) != 3 || aws.ToString(all.NextToken) != "" {
		t.Fatalf("single page = %d urls token=%q, want 3 no token", len(all.QueueUrls), aws.ToString(all.NextToken))
	}
}
