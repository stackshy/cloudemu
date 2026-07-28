package bedrock_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

func TestSDKTagResourceRoundtrip(t *testing.T) {
	client := newControlClient(t)
	ctx := context.Background()

	created, err := client.CreateGuardrail(ctx, &awsbedrock.CreateGuardrailInput{
		Name:                    aws.String("gr-tags"),
		BlockedInputMessaging:   aws.String("blocked input"),
		BlockedOutputsMessaging: aws.String("blocked output"),
	})
	if err != nil {
		t.Fatalf("CreateGuardrail: %v", err)
	}

	arn := aws.ToString(created.GuardrailArn)
	if arn == "" {
		t.Fatal("expected a guardrail ARN")
	}

	if _, err = client.TagResource(ctx, &awsbedrock.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags: []bedrocktypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("team"), Value: aws.String("ml")},
		},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	list, err := client.ListTagsForResource(ctx, &awsbedrock.ListTagsForResourceInput{
		ResourceARN: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if got := tagValue(list.Tags, "env"); got != "prod" {
		t.Fatalf("env tag = %q, want prod (tags: %+v)", got, list.Tags)
	}

	if got := tagValue(list.Tags, "team"); got != "ml" {
		t.Fatalf("team tag = %q, want ml (tags: %+v)", got, list.Tags)
	}

	if _, err = client.UntagResource(ctx, &awsbedrock.UntagResourceInput{
		ResourceARN: aws.String(arn),
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	after, err := client.ListTagsForResource(ctx, &awsbedrock.ListTagsForResourceInput{
		ResourceARN: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource after untag: %v", err)
	}

	if got := tagValue(after.Tags, "env"); got != "" {
		t.Fatalf("expected env tag to be removed, still got %q", got)
	}

	if got := tagValue(after.Tags, "team"); got != "ml" {
		t.Fatalf("team tag = %q, want ml after untag", got)
	}
}

func tagValue(tags []bedrocktypes.Tag, key string) string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == key {
			return aws.ToString(tag.Value)
		}
	}

	return ""
}
