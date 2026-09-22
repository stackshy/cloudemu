package cloudfront_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscf "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// TestSDKTaggingMissingResourceIsNoSuchResource covers that the
// resource-type-agnostic tagging API answers NoSuchResource (not
// NoSuchDistribution) for an ARN naming nothing.
func TestSDKTaggingMissingResourceIsNoSuchResource(t *testing.T) {
	client := newCloudFrontClient(t)
	ctx := context.Background()

	const arn = "arn:aws:cloudfront::123456789012:distribution/EMISSING0000"

	requireNoSuchResource := func(op string, err error) {
		t.Helper()

		var nsr *cftypes.NoSuchResource
		if !errors.As(err, &nsr) {
			t.Fatalf("%s: err = %v, want NoSuchResource", op, err)
		}
	}

	_, err := client.ListTagsForResource(ctx, &awscf.ListTagsForResourceInput{Resource: aws.String(arn)})
	requireNoSuchResource("ListTagsForResource", err)

	_, err = client.TagResource(ctx, &awscf.TagResourceInput{
		Resource: aws.String(arn),
		Tags:     &cftypes.Tags{Items: []cftypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}},
	})
	requireNoSuchResource("TagResource", err)

	_, err = client.UntagResource(ctx, &awscf.UntagResourceInput{
		Resource: aws.String(arn),
		TagKeys:  &cftypes.TagKeys{Items: []string{"k"}},
	})
	requireNoSuchResource("UntagResource", err)
}
