package keyspaces_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskeyspaces "github.com/aws/aws-sdk-go-v2/service/keyspaces"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
)

// Tagging a keyspace/table ARN that doesn't exist is ResourceNotFoundException.
func TestSDKTagsOnMissingResource(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	ghost := aws.String("arn:aws:cassandra:us-east-1:123456789012:/keyspace/ghost/")
	tags := []kstypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}

	var rnf *kstypes.ResourceNotFoundException

	_, err := client.TagResource(ctx, &awskeyspaces.TagResourceInput{ResourceArn: ghost, Tags: tags})
	if !errors.As(err, &rnf) {
		t.Fatalf("TagResource: got %v, want ResourceNotFoundException", err)
	}

	_, err = client.UntagResource(ctx, &awskeyspaces.UntagResourceInput{ResourceArn: ghost, Tags: tags})
	if !errors.As(err, &rnf) {
		t.Fatalf("UntagResource: got %v, want ResourceNotFoundException", err)
	}

	_, err = client.ListTagsForResource(ctx, &awskeyspaces.ListTagsForResourceInput{ResourceArn: ghost})
	if !errors.As(err, &rnf) {
		t.Fatalf("ListTagsForResource: got %v, want ResourceNotFoundException", err)
	}

	var ve *kstypes.ValidationException

	_, err = client.TagResource(ctx, &awskeyspaces.TagResourceInput{
		ResourceArn: aws.String("arn:aws:dynamodb:us-east-1:123456789012:table/x"), Tags: tags,
	})
	if !errors.As(err, &ve) {
		t.Fatalf("TagResource non-cassandra ARN: got %v, want ValidationException", err)
	}
}

// Tag restrictions over the wire: reserved aws: prefix, empty value, and more
// than 60 tags in one request are ValidationException; the system_schema_mcs
// keyspace exists and can't be deleted.
func TestSDKTagRestrictionsAndSystemKeyspace(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")})
	if err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	var ve *kstypes.ValidationException

	for _, tag := range []kstypes.Tag{
		{Key: aws.String("aws:owner"), Value: aws.String("v")},
		{Key: aws.String("k"), Value: aws.String("")},
	} {
		_, err = client.TagResource(ctx, &awskeyspaces.TagResourceInput{ResourceArn: out.ResourceArn, Tags: []kstypes.Tag{tag}})
		if !errors.As(err, &ve) {
			t.Fatalf("TagResource %q: got %v, want ValidationException", aws.ToString(tag.Key), err)
		}
	}

	many := make([]kstypes.Tag, 61)
	for i := range many {
		many[i] = kstypes.Tag{Key: aws.String("k" + strconv.Itoa(i)), Value: aws.String("v")}
	}

	_, err = client.TagResource(ctx, &awskeyspaces.TagResourceInput{ResourceArn: out.ResourceArn, Tags: many})
	if !errors.As(err, &ve) {
		t.Fatalf("TagResource 61 tags: got %v, want ValidationException", err)
	}

	_, err = client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app2"), Tags: many})
	if !errors.As(err, &ve) {
		t.Fatalf("CreateKeyspace 61 tags: got %v, want ValidationException", err)
	}

	if _, err := client.GetKeyspace(ctx, &awskeyspaces.GetKeyspaceInput{KeyspaceName: aws.String("system_schema_mcs")}); err != nil {
		t.Fatalf("GetKeyspace system_schema_mcs: %v", err)
	}

	_, err = client.DeleteKeyspace(ctx, &awskeyspaces.DeleteKeyspaceInput{KeyspaceName: aws.String("system_schema_mcs")})
	if !errors.As(err, &ve) {
		t.Fatalf("DeleteKeyspace system_schema_mcs: got %v, want ValidationException", err)
	}
}
