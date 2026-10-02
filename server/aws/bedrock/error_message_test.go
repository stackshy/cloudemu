package bedrock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

// TestSDKNotFoundMessageOmitsInternalCodePrefix covers that Bedrock error
// responses carry only the human message, not the internal "NotFound: "
// error-code prefix.
func TestSDKNotFoundMessageOmitsInternalCodePrefix(t *testing.T) {
	client := newControlClient(t)

	_, err := client.GetCustomModel(context.Background(), &awsbedrock.GetCustomModelInput{
		ModelIdentifier: aws.String("missing-model"),
	})

	var nf *bedrocktypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("GetCustomModel missing: err = %v, want ResourceNotFoundException", err)
	}

	if got := nf.ErrorMessage(); got != `custom model "missing-model" not found` {
		t.Fatalf("message = %q, want the bare human message", got)
	}
}
