package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/smithy-go"
)

// TestSDKLambdaErrorMessagesHaveNoInternalPrefix pins that Lambda wire error
// messages carry only the human sentence — not the internal "NotFound:" code
// prefix that cerrors.Error.Error() prepends.
func TestSDKLambdaErrorMessagesHaveNoInternalPrefix(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("nope"),
	})
	if err == nil {
		t.Fatal("GetFunction on a missing function: want error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an API error, got %v", err)
	}

	if apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Errorf("error code = %q, want ResourceNotFoundException", apiErr.ErrorCode())
	}

	msg := apiErr.ErrorMessage()
	for _, p := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:"} {
		if strings.HasPrefix(msg, p) {
			t.Errorf("wire message %q leaks internal error-code prefix %q", msg, p)
		}
	}
}
