package ssm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// TestSDKPutParameterTagsOnCreate verifies that Tags supplied inline on
// PutParameter (create) are persisted and returned by ListTagsForResource.
// Regression for the Terraform tag-drift bug where create-time Tags were
// silently dropped.
func TestSDKPutParameterTagsOnCreate(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/tagged"),
		Value: aws.String("v"),
		Type:  ssmtypes.ParameterTypeString,
		Tags: []ssmtypes.Tag{
			{Key: aws.String("Env"), Value: aws.String("prod")},
		},
	}); err != nil {
		t.Fatalf("PutParameter(create with tags): %v", err)
	}

	list, err := client.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String("/app/tagged"),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(list.TagList) != 1 ||
		aws.ToString(list.TagList[0].Key) != "Env" ||
		aws.ToString(list.TagList[0].Value) != "prod" {
		t.Fatalf("TagList = %+v, want [{Env prod}]", list.TagList)
	}
}

// TestSDKPutParameterOverwriteWithTagsRejected verifies that supplying Tags
// together with Overwrite=true is rejected with ValidationException, matching
// real Parameter Store.
func TestSDKPutParameterOverwriteWithTagsRejected(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/ot"),
		Value: aws.String("v1"),
		Type:  ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:      aws.String("/app/ot"),
		Value:     aws.String("v2"),
		Overwrite: aws.Bool(true),
		Tags: []ssmtypes.Tag{
			{Key: aws.String("K"), Value: aws.String("V")},
		},
	})
	if err == nil {
		t.Fatal("PutParameter(overwrite+tags): expected error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("error code = %q, want ValidationException", apiErr.ErrorCode())
	}
}
