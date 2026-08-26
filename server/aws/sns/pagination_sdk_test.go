package sns_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// TestSDKListSubscriptionsByTopicPagination walks ListSubscriptionsByTopic across
// pages. SNS returns a fixed 100 per page, so 101 subscriptions yield a full page
// with a token then a final page of one without one, each subscription once.
func TestSDKListSubscriptionsByTopicPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("paged")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	const total = 101
	for i := range total {
		if _, err := client.Subscribe(ctx, &awssns.SubscribeInput{
			TopicArn:              topic.TopicArn,
			Protocol:              aws.String("email"),
			Endpoint:              aws.String(fmt.Sprintf("u%d@example.com", i)),
			ReturnSubscriptionArn: true,
		}); err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
	}

	page1, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: topic.TopicArn,
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic page1: %v", err)
	}

	if len(page1.Subscriptions) != 100 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d subs token=%q, want 100 with token", len(page1.Subscriptions), aws.ToString(page1.NextToken))
	}

	page2, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: topic.TopicArn, NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic page2: %v", err)
	}

	if len(page2.Subscriptions) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d subs token=%q, want 1 no token", len(page2.Subscriptions), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, s := range append(page1.Subscriptions, page2.Subscriptions...) {
		arn := aws.ToString(s.SubscriptionArn)
		if seen[arn] {
			t.Fatalf("subscription %q returned twice across pages", arn)
		}

		seen[arn] = true
	}

	if len(seen) != total {
		t.Fatalf("walked %d unique subscriptions, want %d", len(seen), total)
	}

	// A topic with a single page of subscriptions returns no token.
	small, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("small")})
	if err != nil {
		t.Fatalf("CreateTopic small: %v", err)
	}

	if _, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: small.TopicArn, Protocol: aws.String("email"),
		Endpoint: aws.String("one@example.com"), ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatalf("Subscribe small: %v", err)
	}

	one, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: small.TopicArn,
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic small: %v", err)
	}

	if len(one.Subscriptions) != 1 || aws.ToString(one.NextToken) != "" {
		t.Fatalf("single page = %d subs token=%q, want 1 no token", len(one.Subscriptions), aws.ToString(one.NextToken))
	}
}
