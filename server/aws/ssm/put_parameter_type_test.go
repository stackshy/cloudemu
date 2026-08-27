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

// TestSDKPutParameterOverwriteRetainsType verifies that an Overwrite update
// that omits Type keeps the parameter's existing type. Real Parameter Store
// doesn't require a type when updating: a SecureString stays SecureString.
func TestSDKPutParameterOverwriteRetainsType(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/secret"),
		Value: aws.String("s3cr3t"),
		Type:  ssmtypes.ParameterTypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	// Overwrite without re-sending Type.
	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:      aws.String("/app/secret"),
		Value:     aws.String("s3cr3t-v2"),
		Overwrite: aws.Bool(true),
	}); err != nil {
		t.Fatalf("PutParameter(overwrite): %v", err)
	}

	got, err := client.GetParameter(ctx, &awsssm.GetParameterInput{
		Name:           aws.String("/app/secret"),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.Parameter.Type != ssmtypes.ParameterTypeSecureString {
		t.Fatalf("Type = %q, want SecureString (retained on Overwrite)", got.Parameter.Type)
	}

	if aws.ToString(got.Parameter.Value) != "s3cr3t-v2" {
		t.Fatalf("Value = %q, want s3cr3t-v2", aws.ToString(got.Parameter.Value))
	}
}

// TestSDKPutParameterInvalidTypeRejected verifies that an unrecognized Type is
// rejected with UnsupportedParameterType rather than being silently coerced to
// String.
func TestSDKPutParameterInvalidTypeRejected(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/badtype"),
		Value: aws.String("v"),
		Type:  ssmtypes.ParameterType("Bogus"),
	})
	if err == nil {
		t.Fatal("PutParameter(invalid type): expected error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "UnsupportedParameterType" {
		t.Fatalf("error code = %q, want UnsupportedParameterType", apiErr.ErrorCode())
	}

	// The parameter must not have been stored.
	_, err = client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/badtype")})

	var notFound *ssmtypes.ParameterNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("GetParameter after rejected put: got %v, want ParameterNotFound", err)
	}
}

// TestSDKPutParameterValidTypesAccepted verifies each recognized Type is
// accepted (regression guard for the invalid-type validation).
func TestSDKPutParameterValidTypesAccepted(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	cases := []struct {
		name string
		typ  ssmtypes.ParameterType
	}{
		{"/valid/str", ssmtypes.ParameterTypeString},
		{"/valid/list", ssmtypes.ParameterTypeStringList},
		{"/valid/secure", ssmtypes.ParameterTypeSecureString},
	}

	for _, tc := range cases {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name:  aws.String(tc.name),
			Value: aws.String("a,b"),
			Type:  tc.typ,
		}); err != nil {
			t.Fatalf("PutParameter(%s): %v", tc.typ, err)
		}
	}
}

// TestSDKPutParameterOverwriteSameTypeAllowed verifies that re-sending the same
// type on an Overwrite update is accepted.
func TestSDKPutParameterOverwriteSameTypeAllowed(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/flag"),
		Value: aws.String("a"),
		Type:  ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:      aws.String("/app/flag"),
		Value:     aws.String("b"),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: aws.Bool(true),
	}); err != nil {
		t.Fatalf("PutParameter(overwrite same type): %v", err)
	}
}

// TestSDKPutParameterOverwriteChangeTypeRejected verifies that changing the type
// on an Overwrite update fails with HierarchyTypeMismatchException.
func TestSDKPutParameterOverwriteChangeTypeRejected(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/plain"),
		Value: aws.String("v1"),
		Type:  ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:      aws.String("/app/plain"),
		Value:     aws.String("v2"),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	})
	if err == nil {
		t.Fatal("PutParameter(change type): expected error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	if apiErr.ErrorCode() != "HierarchyTypeMismatchException" {
		t.Fatalf("error code = %q, want HierarchyTypeMismatchException", apiErr.ErrorCode())
	}

	// The type must not have changed.
	got, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/plain")})
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.Parameter.Type != ssmtypes.ParameterTypeString {
		t.Fatalf("Type = %q, want String (unchanged after rejected type change)", got.Parameter.Type)
	}
}
