package eks_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/smithy-go"
)

// TestSDKEKSErrorMessageNoCodePrefix guards that generic errors surface a clean
// message without the internal canonical-code prefix (e.g. "NotFound: ...").
// The wire ErrorType header still carries the typed code.
func TestSDKEKSErrorMessageNoCodePrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("nope")})
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected smithy.APIError, got %T: %v", err, err)
	}

	if apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("error code = %q, want ResourceNotFoundException", apiErr.ErrorCode())
	}

	if msg := apiErr.ErrorMessage(); strings.Contains(msg, "NotFound:") {
		t.Fatalf("error message leaks canonical-code prefix: %q", msg)
	}
}
