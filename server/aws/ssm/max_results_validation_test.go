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

// TestSDKGetParametersByPathMaxResultsOverCeiling verifies MaxResults above the
// validated ceiling (10 for GetParametersByPath) is rejected with a
// ValidationException rather than being silently clamped down.
func TestSDKGetParametersByPathMaxResultsOverCeiling(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/app/db/host"), Value: aws.String("h"), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	_, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path:       aws.String("/app/"),
		Recursive:  aws.Bool(true),
		MaxResults: aws.Int32(50),
	})
	if err == nil {
		t.Fatal("GetParametersByPath MaxResults=50 succeeded, want ValidationException")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("got error %v, want ValidationException", err)
	}

	// The ceiling itself (10) is accepted.
	if _, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path:       aws.String("/app/"),
		Recursive:  aws.Bool(true),
		MaxResults: aws.Int32(10),
	}); err != nil {
		t.Fatalf("GetParametersByPath MaxResults=10: %v", err)
	}
}

// TestSDKDescribeParametersMaxResultsOverCeiling verifies DescribeParameters
// rejects MaxResults above its ceiling (50).
func TestSDKDescribeParametersMaxResultsOverCeiling(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/d/one"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	_, err := client.DescribeParameters(ctx, &awsssm.DescribeParametersInput{
		MaxResults: aws.Int32(51),
	})
	if err == nil {
		t.Fatal("DescribeParameters MaxResults=51 succeeded, want ValidationException")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("got error %v, want ValidationException", err)
	}

	// The ceiling itself (50) is accepted.
	if _, err := client.DescribeParameters(ctx, &awsssm.DescribeParametersInput{
		MaxResults: aws.Int32(50),
	}); err != nil {
		t.Fatalf("DescribeParameters MaxResults=50: %v", err)
	}
}
