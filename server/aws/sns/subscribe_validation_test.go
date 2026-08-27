package sns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	smithy "github.com/aws/smithy-go"
)

// TestSDKSubscribeInvalidProtocol covers finding 5: Subscribe with a protocol
// outside the supported set must reject with InvalidParameter, not create a
// subscription.
func TestSDKSubscribeInvalidProtocol(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("proto-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	_, err = client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("carrier-pigeon"),
		Endpoint: aws.String("nowhere"),
	})
	if err == nil {
		t.Fatal("Subscribe with invalid protocol succeeded, want InvalidParameter")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy.APIError: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidParameter" {
		t.Fatalf("error code = %q, want InvalidParameter", apiErr.ErrorCode())
	}
}

// TestSDKSubscribeValidProtocol guards the happy path: a supported protocol still
// creates a subscription.
func TestSDKSubscribeValidProtocol(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	topic, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("ok-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	out, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn,
		Protocol: aws.String("email"),
		Endpoint: aws.String("dev@example.com"),
	})
	if err != nil {
		t.Fatalf("Subscribe with valid protocol: %v", err)
	}

	if aws.ToString(out.SubscriptionArn) == "" {
		t.Fatal("Subscribe returned empty SubscriptionArn")
	}
}
