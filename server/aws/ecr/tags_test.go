package ecr_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKECRListTagsForResourceOrderDeterministic guards against
// ListTagsForResource returning tags in randomized order (a Go map iterates
// in randomized order, and the tag store is a map keyed by tag). A caller
// diffing repeated reads of unchanged tags must see a stable order.
func TestSDKECRListTagsForResourceOrderDeterministic(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	arn := "arn:aws:ecr:us-east-1:123456789012:repository/tag-order-repo"

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("tag-order-repo"),
		Tags: []ecrtypes.Tag{
			{Key: aws.String("a"), Value: aws.String("1")},
			{Key: aws.String("b"), Value: aws.String("2")},
			{Key: aws.String("c"), Value: aws.String("3")},
			{Key: aws.String("d"), Value: aws.String("4")},
			{Key: aws.String("e"), Value: aws.String("5")},
		},
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	var first []string

	for i := range 5 {
		got, err := client.ListTagsForResource(ctx, &awsecr.ListTagsForResourceInput{
			ResourceArn: aws.String(arn),
		})
		if err != nil {
			t.Fatalf("ListTagsForResource[%d]: %v", i, err)
		}

		keys := make([]string, len(got.Tags))
		for j, tag := range got.Tags {
			keys[j] = aws.ToString(tag.Key)
		}

		if i == 0 {
			first = keys
			continue
		}

		if len(keys) != len(first) {
			t.Fatalf("ListTagsForResource[%d] returned %d tags, want %d", i, len(keys), len(first))
		}

		for j := range keys {
			if keys[j] != first[j] {
				t.Fatalf("ListTagsForResource order is nondeterministic: call 0 = %v, call %d = %v", first, i, keys)
			}
		}
	}
}
