package sns_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"

	"github.com/stackshy/cloudemu/v2"
	snsprovider "github.com/stackshy/cloudemu/v2/providers/aws/sns"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	snspkg "github.com/stackshy/cloudemu/v2/server/aws/sns"
)

func newSDKClient(t *testing.T) *awssns.Client {
	t.Helper()

	client, _ := newSDKServer(t)

	return client
}

// newSDKServer stands up an in-process SNS wire server and returns both the SDK
// client and the backing provider mock (so tests can read internal state such as
// a pending subscription's confirmation token).
func newSDKServer(t *testing.T) (*awssns.Client, *snsprovider.Mock) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		SNS: cloud.SNS,
		// RDS is wired (and registers before SNS) so the SNS-scoped
		// ListTagsForResource genuinely competes with RDS on the shared query
		// wire; EC2 is the query-protocol catch-all.
		RDS: cloud.RDS,
		EC2: cloud.EC2,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awssns.NewFromConfig(cfg, func(o *awssns.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, cloud.SNS
}

func TestSDKSNSTopicLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("my-topic"),
		Tags: []snstypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)
	if topicArn == "" {
		t.Fatal("CreateTopic returned empty TopicArn")
	}

	// CreateTopic is idempotent: a second create with the same name returns
	// the same ARN rather than an error.
	again, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("my-topic")})
	if err != nil {
		t.Fatalf("CreateTopic (idempotent): %v", err)
	}

	if aws.ToString(again.TopicArn) != topicArn {
		t.Fatalf("idempotent CreateTopic ARN = %q, want %q", aws.ToString(again.TopicArn), topicArn)
	}

	attrs, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{
		TopicArn: aws.String(topicArn),
	})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}

	if attrs.Attributes["TopicArn"] != topicArn {
		t.Fatalf("attribute TopicArn = %q, want %q", attrs.Attributes["TopicArn"], topicArn)
	}

	sub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("email"),
		Endpoint:              aws.String("ops@example.com"),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subArn := aws.ToString(sub.SubscriptionArn)
	if subArn == "" {
		t.Fatal("Subscribe returned empty SubscriptionArn")
	}

	pub, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String(topicArn),
		Subject:  aws.String("hello"),
		Message:  aws.String("world"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if aws.ToString(pub.MessageId) == "" {
		t.Fatal("Publish returned empty MessageId")
	}

	byTopic, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: aws.String(topicArn),
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic: %v", err)
	}

	if len(byTopic.Subscriptions) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(byTopic.Subscriptions))
	}

	if aws.ToString(byTopic.Subscriptions[0].Protocol) != "email" ||
		aws.ToString(byTopic.Subscriptions[0].Endpoint) != "ops@example.com" {
		t.Fatalf("subscription = %+v, want email/ops@example.com", byTopic.Subscriptions[0])
	}

	list, err := client.ListTopics(ctx, &awssns.ListTopicsInput{})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}

	if len(list.Topics) != 1 || aws.ToString(list.Topics[0].TopicArn) != topicArn {
		t.Fatalf("ListTopics = %+v, want one topic %q", list.Topics, topicArn)
	}

	allSubs, err := client.ListSubscriptions(ctx, &awssns.ListSubscriptionsInput{})
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}

	if len(allSubs.Subscriptions) != 1 {
		t.Fatalf("ListSubscriptions got %d, want 1", len(allSubs.Subscriptions))
	}

	if _, err := client.Unsubscribe(ctx, &awssns.UnsubscribeInput{
		SubscriptionArn: aws.String(subArn),
	}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	afterUnsub, err := client.ListSubscriptionsByTopic(ctx, &awssns.ListSubscriptionsByTopicInput{
		TopicArn: aws.String(topicArn),
	})
	if err != nil {
		t.Fatalf("ListSubscriptionsByTopic (after unsubscribe): %v", err)
	}

	if len(afterUnsub.Subscriptions) != 0 {
		t.Fatalf("got %d subscriptions after unsubscribe, want 0", len(afterUnsub.Subscriptions))
	}

	if _, err := client.DeleteTopic(ctx, &awssns.DeleteTopicInput{
		TopicArn: aws.String(topicArn),
	}); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
}

func TestSDKSNSErrors(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:missing"),
	})
	if err == nil {
		t.Fatal("GetTopicAttributes(missing): expected error, got nil")
	}

	var nfe *snstypes.NotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("GetTopicAttributes(missing): got %v, want NotFoundException", err)
	}

	if _, err := client.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:missing"),
		Message:  aws.String("x"),
	}); !errors.As(err, &nfe) {
		t.Fatalf("Publish(missing topic): got %v, want NotFoundException", err)
	}
}

// TestSDKSNSListTagsAndPolicy guards the two IaC read-path fixes: ListTagsForResource
// must be claimed by SNS (via its SigV4 scope, not RDS which registers first) and
// echo the tag, and GetTopicAttributes must return a valid-JSON default Policy.
func TestSDKSNSListTagsAndPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
		Name: aws.String("tagged"),
		Tags: []snstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	arn := aws.ToString(created.TopicArn)

	tags, err := client.ListTagsForResource(ctx, &awssns.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" || aws.ToString(tags.Tags[0].Value) != "prod" {
		t.Fatalf("tags = %+v, want env=prod", tags.Tags)
	}

	attrs, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}

	policy := attrs.Attributes["Policy"]
	if policy == "" {
		t.Fatal("Policy attribute is empty")
	}

	var js map[string]any
	if err := json.Unmarshal([]byte(policy), &js); err != nil {
		t.Fatalf("Policy is not valid JSON: %v\n%s", err, policy)
	}

	// Owner must be the ARN's account id (also the policy's SourceOwner).
	if want := strings.Split(arn, ":")[4]; attrs.Attributes["Owner"] != want {
		t.Fatalf("Owner = %q, want %q", attrs.Attributes["Owner"], want)
	}
}

// TestSNSMatchesScopeGatesListTags asserts the SigV4-scope gating both ways:
// SNS claims ListTagsForResource only for an sns-signed request, and declines an
// rds-signed one (which RDS, registered first on the shared query wire, owns).
func TestSNSMatchesScopeGatesListTags(t *testing.T) {
	h := snspkg.New(cloudemu.NewAWS().SNS)
	body := "Action=ListTagsForResource&ResourceArn=arn:aws:sns:us-east-1:123456789012:t"

	req := func(service string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/"+service+"/aws4_request, SignedHeaders=host, Signature=x")

		return r
	}

	if !h.Matches(req("sns")) {
		t.Error("sns-scoped ListTagsForResource should be claimed by SNS")
	}

	if h.Matches(req("rds")) {
		t.Error("rds-scoped ListTagsForResource must NOT be claimed by SNS")
	}
}

// TestSDKSNSSubscriptionAttributes covers Subscribe carrying FilterPolicy /
// RawMessageDelivery, GetSubscriptionAttributes surfacing them, and
// SetSubscriptionAttributes mutating one — none of which the handler modeled
// before (both attribute ops were undispatched → InvalidAction).
func TestSDKSNSSubscriptionAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("attr-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	sub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String("arn:aws:sqs:us-east-1:000000000000:q"),
		ReturnSubscriptionArn: true,
		Attributes: map[string]string{
			"FilterPolicy":       `{"color":["red"]}`,
			"RawMessageDelivery": "true",
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subArn := aws.ToString(sub.SubscriptionArn)

	got, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subArn),
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes: %v", err)
	}

	if got.Attributes["FilterPolicy"] != `{"color":["red"]}` {
		t.Fatalf("FilterPolicy = %q, want the subscribe-time policy", got.Attributes["FilterPolicy"])
	}

	if got.Attributes["RawMessageDelivery"] != "true" {
		t.Fatalf("RawMessageDelivery = %q, want true", got.Attributes["RawMessageDelivery"])
	}

	if got.Attributes["PendingConfirmation"] != "false" {
		t.Fatalf("PendingConfirmation = %q, want false (sqs auto-confirms)", got.Attributes["PendingConfirmation"])
	}

	if got.Attributes["TopicArn"] != topicArn || got.Attributes["Protocol"] != "sqs" {
		t.Fatalf("attrs TopicArn/Protocol = %q/%q", got.Attributes["TopicArn"], got.Attributes["Protocol"])
	}

	if _, err := client.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subArn),
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(`{"color":["blue"]}`),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes: %v", err)
	}

	got2, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subArn),
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes (after set): %v", err)
	}

	if got2.Attributes["FilterPolicy"] != `{"color":["blue"]}` {
		t.Fatalf("FilterPolicy after set = %q, want the updated policy", got2.Attributes["FilterPolicy"])
	}
}

// TestSDKSNSPendingConfirmationCounts asserts that confirmation-required
// protocols return the literal "pending confirmation" ARN and are counted as
// pending (not confirmed) by GetTopicAttributes.
func TestSDKSNSPendingConfirmationCounts(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("pending-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	emailSub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: aws.String(topicArn),
		Protocol: aws.String("email"),
		Endpoint: aws.String("ops@example.com"),
	})
	if err != nil {
		t.Fatalf("Subscribe(email): %v", err)
	}

	if aws.ToString(emailSub.SubscriptionArn) != "pending confirmation" {
		t.Fatalf("email SubscriptionArn = %q, want \"pending confirmation\"", aws.ToString(emailSub.SubscriptionArn))
	}

	if _, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: aws.String(topicArn),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:us-east-1:000000000000:q"),
	}); err != nil {
		t.Fatalf("Subscribe(sqs): %v", err)
	}

	attrs, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(topicArn)})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}

	if attrs.Attributes["SubscriptionsPending"] != "1" {
		t.Fatalf("SubscriptionsPending = %q, want 1", attrs.Attributes["SubscriptionsPending"])
	}

	if attrs.Attributes["SubscriptionsConfirmed"] != "1" {
		t.Fatalf("SubscriptionsConfirmed = %q, want 1 (only the sqs sub)", attrs.Attributes["SubscriptionsConfirmed"])
	}

	if attrs.Attributes["EffectiveDeliveryPolicy"] == "" {
		t.Fatal("EffectiveDeliveryPolicy is absent")
	}

	var edp map[string]any
	if err := json.Unmarshal([]byte(attrs.Attributes["EffectiveDeliveryPolicy"]), &edp); err != nil {
		t.Fatalf("EffectiveDeliveryPolicy is not valid JSON: %v", err)
	}
}

// TestSDKSNSConfirmSubscription drives a pending email subscription through
// ConfirmSubscription (previously undispatched → InvalidAction) using the token
// the mock generated, and asserts the subscription flips to confirmed.
func TestSDKSNSConfirmSubscription(t *testing.T) {
	client, mock := newSDKServer(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("confirm-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	sub, err := client.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn:              aws.String(topicArn),
		Protocol:              aws.String("email"),
		Endpoint:              aws.String("ops@example.com"),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	subArn := aws.ToString(sub.SubscriptionArn)

	// The confirmation token is delivered out-of-band by real SNS; read it from
	// the mock to complete the round trip.
	pending, err := mock.GetSubscription(ctx, subArn)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}

	if pending.ConfirmationToken == "" {
		t.Fatal("pending subscription has no confirmation token")
	}

	confirmed, err := client.ConfirmSubscription(ctx, &awssns.ConfirmSubscriptionInput{
		TopicArn: aws.String(topicArn),
		Token:    aws.String(pending.ConfirmationToken),
	})
	if err != nil {
		t.Fatalf("ConfirmSubscription: %v", err)
	}

	if aws.ToString(confirmed.SubscriptionArn) != subArn {
		t.Fatalf("ConfirmSubscription arn = %q, want %q", aws.ToString(confirmed.SubscriptionArn), subArn)
	}

	attrs, err := client.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subArn),
	})
	if err != nil {
		t.Fatalf("GetSubscriptionAttributes: %v", err)
	}

	if attrs.Attributes["PendingConfirmation"] != "false" {
		t.Fatalf("PendingConfirmation = %q, want false after confirm", attrs.Attributes["PendingConfirmation"])
	}
}

// TestSDKSNSPublishBatch asserts PublishBatch (previously undispatched) fans out
// to per-entry results with distinct message ids.
func TestSDKSNSPublishBatch(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("batch-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	out, err := client.PublishBatch(ctx, &awssns.PublishBatchInput{
		TopicArn: aws.String(topicArn),
		PublishBatchRequestEntries: []snstypes.PublishBatchRequestEntry{
			{Id: aws.String("a"), Message: aws.String("one")},
			{Id: aws.String("b"), Message: aws.String("two")},
		},
	})
	if err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}

	if len(out.Successful) != 2 {
		t.Fatalf("got %d successful, want 2 (failed=%v)", len(out.Successful), out.Failed)
	}

	ids := map[string]string{}
	for _, s := range out.Successful {
		if aws.ToString(s.MessageId) == "" {
			t.Fatalf("entry %q has empty MessageId", aws.ToString(s.Id))
		}

		ids[aws.ToString(s.Id)] = aws.ToString(s.MessageId)
	}

	if ids["a"] == "" || ids["b"] == "" || ids["a"] == ids["b"] {
		t.Fatalf("batch message ids = %v, want two distinct ids for a and b", ids)
	}
}

// TestSDKSNSAddRemovePermission asserts AddPermission / RemovePermission
// (previously undispatched) mutate the topic access policy.
func TestSDKSNSAddRemovePermission(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("perm-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	topicArn := aws.ToString(created.TopicArn)

	if _, err := client.AddPermission(ctx, &awssns.AddPermissionInput{
		TopicArn:     aws.String(topicArn),
		Label:        aws.String("share-publish"),
		AWSAccountId: []string{"111122223333"},
		ActionName:   []string{"Publish"},
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	attrs, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(topicArn)})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}

	if !strings.Contains(attrs.Attributes["Policy"], "share-publish") ||
		!strings.Contains(attrs.Attributes["Policy"], "111122223333") {
		t.Fatalf("policy after AddPermission missing statement: %s", attrs.Attributes["Policy"])
	}

	if _, err := client.RemovePermission(ctx, &awssns.RemovePermissionInput{
		TopicArn: aws.String(topicArn),
		Label:    aws.String("share-publish"),
	}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}

	attrs2, err := client.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(topicArn)})
	if err != nil {
		t.Fatalf("GetTopicAttributes (after remove): %v", err)
	}

	if strings.Contains(attrs2.Attributes["Policy"], "share-publish") {
		t.Fatalf("policy still has removed statement: %s", attrs2.Attributes["Policy"])
	}
}

// TestSDKSNSListTopicsPagination asserts ListTopics returns a NextToken and
// pages through results once the topic count exceeds one page.
func TestSDKSNSListTopicsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const total = 101

	for i := 0; i < total; i++ {
		if _, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
			Name: aws.String("page-topic-" + strconv.Itoa(i)),
		}); err != nil {
			t.Fatalf("CreateTopic %d: %v", i, err)
		}
	}

	first, err := client.ListTopics(ctx, &awssns.ListTopicsInput{})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}

	if len(first.Topics) != 100 {
		t.Fatalf("first page = %d topics, want 100", len(first.Topics))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatal("first page returned no NextToken")
	}

	second, err := client.ListTopics(ctx, &awssns.ListTopicsInput{NextToken: first.NextToken})
	if err != nil {
		t.Fatalf("ListTopics (page 2): %v", err)
	}

	if len(second.Topics) != total-100 {
		t.Fatalf("second page = %d topics, want %d", len(second.Topics), total-100)
	}

	if aws.ToString(second.NextToken) != "" {
		t.Fatalf("second page NextToken = %q, want empty", aws.ToString(second.NextToken))
	}
}
