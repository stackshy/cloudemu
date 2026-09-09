package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// TestSDKSNSConfirmationWasAuthenticated asserts GetSubscriptionAttributes
// reports ConfirmationWasAuthenticated="true" for a subscription confirmed as
// part of the authenticated Subscribe call (sqs and the other auto-confirm
// protocols), and "false" for a still-pending confirmation-required protocol.
// Real SNS documents ConfirmationWasAuthenticated as "true if the subscription
// confirmation request was authenticated".
func TestSDKSNSConfirmationWasAuthenticated(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("cwa-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	// SQS auto-confirms via the authenticated Subscribe API -> "true".
	sqsSub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String("arn:aws:sqs:us-east-1:000000000000:q"),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe(sqs): %v", err)
	}

	got, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: sqsSub.SubscriptionArn,
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes(sqs): %v", err)
	}

	if got.Attributes["ConfirmationWasAuthenticated"] != "true" {
		t.Fatalf("sqs ConfirmationWasAuthenticated = %q, want true",
			got.Attributes["ConfirmationWasAuthenticated"])
	}

	// email requires out-of-band confirmation and stays pending -> "false".
	emailSub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("email"),
		Endpoint:              aws.String("dev@example.com"),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe(email): %v", err)
	}

	gotEmail, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: emailSub.SubscriptionArn,
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes(email): %v", err)
	}

	if gotEmail.Attributes["ConfirmationWasAuthenticated"] != "false" {
		t.Fatalf("pending email ConfirmationWasAuthenticated = %q, want false",
			gotEmail.Attributes["ConfirmationWasAuthenticated"])
	}
}

// TestSDKSNSFilterPolicyScopeDefault asserts GetSubscriptionAttributes surfaces
// the documented default FilterPolicyScope="MessageAttributes" for a
// subscription that has a FilterPolicy but no explicit scope, does not surface
// the scope at all when there is no FilterPolicy, and never overrides an
// explicitly set MessageBody scope.
func TestSDKSNSFilterPolicyScopeDefault(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("fps-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	// No filter policy -> no FilterPolicyScope emitted at all.
	bare, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String("arn:aws:sqs:us-east-1:000000000000:bare"),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe(bare): %v", err)
	}

	gotBare, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: bare.SubscriptionArn,
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes(bare): %v", err)
	}

	if v, ok := gotBare.Attributes["FilterPolicyScope"]; ok {
		t.Fatalf("FilterPolicyScope present without a FilterPolicy: %q", v)
	}

	// Filter policy without an explicit scope -> default "MessageAttributes".
	withPolicy, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String("arn:aws:sqs:us-east-1:000000000000:policy"),
		ReturnSubscriptionArn: true,
		Attributes:            map[string]string{"FilterPolicy": `{"color":["red"]}`},
	})
	if err != nil {
		t.Fatalf("Subscribe(withPolicy): %v", err)
	}

	gotPolicy, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: withPolicy.SubscriptionArn,
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes(withPolicy): %v", err)
	}

	if gotPolicy.Attributes["FilterPolicyScope"] != "MessageAttributes" {
		t.Fatalf("default FilterPolicyScope = %q, want MessageAttributes",
			gotPolicy.Attributes["FilterPolicyScope"])
	}

	// An explicit MessageBody scope is preserved, not clobbered by the default.
	if _, err := client.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: withPolicy.SubscriptionArn,
		AttributeName:   aws.String("FilterPolicyScope"),
		AttributeValue:  aws.String("MessageBody"),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes(scope): %v", err)
	}

	gotBody, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: withPolicy.SubscriptionArn,
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes(body scope): %v", err)
	}

	if gotBody.Attributes["FilterPolicyScope"] != "MessageBody" {
		t.Fatalf("explicit FilterPolicyScope = %q, want MessageBody (default must not override)",
			gotBody.Attributes["FilterPolicyScope"])
	}
}
