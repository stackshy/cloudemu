package cloudfront_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscf "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// TestSDKTaggingForeignARNNamesNothing locks that a tagging ARN naming another
// account, or a string that only ends in distribution/<id>, does not act on
// this account's distribution of that id.
func TestSDKTaggingForeignARNNamesNothing(t *testing.T) {
	client := newCloudFrontClient(t)
	ctx := context.Background()

	out, err := client.CreateDistribution(ctx, &awscf.CreateDistributionInput{
		DistributionConfig: sampleDistConfig("foreign-arn", "c", true),
	})
	if err != nil {
		t.Fatalf("CreateDistribution: %v", err)
	}

	arn := aws.ToString(out.Distribution.ARN)
	id := aws.ToString(out.Distribution.Id)
	foreign := strings.Replace(arn, ":123456789012:", ":999999999999:", 1)

	for _, res := range []string{foreign, "not-an-arn/distribution/" + id, "arn:aws:s3:::b/distribution/" + id} {
		_, err := client.TagResource(ctx, &awscf.TagResourceInput{
			Resource: aws.String(res),
			Tags:     &cftypes.Tags{Items: []cftypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}},
		})
		if code := apiErrorCode(t, err); code != "NoSuchResource" {
			t.Errorf("TagResource %s: code %q, want NoSuchResource", res, code)
		}

		_, err = client.ListTagsForResource(ctx, &awscf.ListTagsForResourceInput{Resource: aws.String(res)})
		if code := apiErrorCode(t, err); code != "NoSuchResource" {
			t.Errorf("ListTagsForResource %s: code %q, want NoSuchResource", res, code)
		}
	}

	tags, err := client.ListTagsForResource(ctx, &awscf.ListTagsForResourceInput{Resource: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if n := len(tags.Tags.Items); n != 0 {
		t.Fatalf("distribution has %d tags after foreign-ARN requests, want 0", n)
	}
}
