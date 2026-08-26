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

// TestSDKGetParametersByPathTypeFilter verifies GetParametersByPath honors a
// ParameterFilters Type=Equals=String filter, returning only String parameters.
func TestSDKGetParametersByPathTypeFilter(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/f/a"), Value: aws.String("1"), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter /f/a: %v", err)
	}

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/f/b"), Value: aws.String("x,y"), Type: ssmtypes.ParameterTypeStringList,
	}); err != nil {
		t.Fatalf("PutParameter /f/b: %v", err)
	}

	got, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path: aws.String("/f"),
		ParameterFilters: []ssmtypes.ParameterStringFilter{
			{Key: aws.String("Type"), Option: aws.String("Equals"), Values: []string{"String"}},
		},
	})
	if err != nil {
		t.Fatalf("GetParametersByPath(Type=String): %v", err)
	}

	if names := paramNames(got.Parameters); !equalStrings(names, []string{"/f/a"}) {
		t.Fatalf("names = %v, want [/f/a]", names)
	}
}

// TestSDKGetParametersByPathInvalidFilterKey verifies that a filter key
// GetParametersByPath doesn't support (e.g. Name) is rejected with
// InvalidFilterKey.
func TestSDKGetParametersByPathInvalidFilterKey(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	_, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path: aws.String("/f"),
		ParameterFilters: []ssmtypes.ParameterStringFilter{
			{Key: aws.String("Name"), Option: aws.String("BeginsWith"), Values: []string{"/f"}},
		},
	})
	if err == nil {
		t.Fatal("GetParametersByPath(Name filter): expected error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidFilterKey" {
		t.Fatalf("error code = %q, want InvalidFilterKey", apiErr.ErrorCode())
	}
}
