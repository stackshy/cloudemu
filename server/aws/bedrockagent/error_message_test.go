package bedrockagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
)

// TestSDKGetAgentNotFoundMessageOmitsInternalCodePrefix covers that GetAgent on
// a missing agent returns ResourceNotFoundException with only the human
// message, not the internal "NotFound: " error-code prefix.
func TestSDKGetAgentNotFoundMessageOmitsInternalCodePrefix(t *testing.T) {
	client := newClient(t)

	_, err := client.GetAgent(context.Background(), &awsba.GetAgentInput{AgentId: aws.String("AGENT00000")})

	var nf *batypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("GetAgent missing: err = %v, want ResourceNotFoundException", err)
	}

	if got := nf.ErrorMessage(); got != `agent "AGENT00000" not found` {
		t.Fatalf("message = %q, want the bare human message", got)
	}
}
