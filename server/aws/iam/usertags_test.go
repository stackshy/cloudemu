package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKUserTagging proves TagUser/ListUserTags/UntagUser round-trip
// (previously undispatched → InvalidAction).
func TestSDKUserTagging(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("tagged")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.TagUser(ctx, &iam.TagUserInput{
		UserName: aws.String("tagged"),
		Tags: []iamtypes.Tag{
			{Key: aws.String("team"), Value: aws.String("platform")},
			{Key: aws.String("env"), Value: aws.String("dev")},
		},
	}); err != nil {
		t.Fatalf("TagUser: %v", err)
	}

	listed, err := client.ListUserTags(ctx, &iam.ListUserTagsInput{UserName: aws.String("tagged")})
	if err != nil {
		t.Fatalf("ListUserTags: %v", err)
	}

	got := map[string]string{}
	for _, tg := range listed.Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	if got["team"] != "platform" || got["env"] != "dev" {
		t.Fatalf("ListUserTags = %v, want team=platform env=dev", got)
	}

	if _, err := client.UntagUser(ctx, &iam.UntagUserInput{
		UserName: aws.String("tagged"), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("UntagUser: %v", err)
	}

	after, err := client.ListUserTags(ctx, &iam.ListUserTagsInput{UserName: aws.String("tagged")})
	if err != nil {
		t.Fatalf("ListUserTags after untag: %v", err)
	}

	if len(after.Tags) != 1 || aws.ToString(after.Tags[0].Key) != "team" {
		t.Fatalf("after untag = %v, want only team", after.Tags)
	}
}
