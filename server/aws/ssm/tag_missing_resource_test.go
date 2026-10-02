package ssm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// TestSDKTaggingMissingResourceIsInvalidResourceID covers that the tagging API
// answers InvalidResourceId (not ParameterNotFound) for a missing resource.
func TestSDKTaggingMissingResourceIsInvalidResourceID(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	requireInvalidResourceID := func(op string, err error) {
		t.Helper()

		var ir *ssmtypes.InvalidResourceId
		if !errors.As(err, &ir) {
			t.Fatalf("%s: err = %v, want InvalidResourceId", op, err)
		}
	}

	_, err := client.AddTagsToResource(ctx, &awsssm.AddTagsToResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String("/missing/param"),
		Tags:         []ssmtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	requireInvalidResourceID("AddTagsToResource", err)

	_, err = client.RemoveTagsFromResource(ctx, &awsssm.RemoveTagsFromResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String("/missing/param"),
		TagKeys:      []string{"k"},
	})
	requireInvalidResourceID("RemoveTagsFromResource", err)

	_, err = client.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String("/missing/param"),
	})
	requireInvalidResourceID("ListTagsForResource", err)
}
