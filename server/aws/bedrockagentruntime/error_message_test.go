package bedrockagentruntime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	agentruntimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

// TestSDKValidationMessageOmitsInternalCodePrefix covers that a
// ValidationException carries only the human message, not the internal
// "InvalidArgument: " error-code prefix.
func TestSDKValidationMessageOmitsInternalCodePrefix(t *testing.T) {
	client := newClient(t)

	_, err := client.Retrieve(context.Background(), &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String("KB12345678"),
		RetrievalQuery:  &agentruntimetypes.KnowledgeBaseQuery{Text: aws.String("")},
	})

	var ve *agentruntimetypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("Retrieve with empty query: err = %v, want ValidationException", err)
	}

	if got := ve.ErrorMessage(); got != "retrievalQuery.text is required" {
		t.Fatalf("message = %q, want the bare human message", got)
	}
}
