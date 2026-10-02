package aws

import (
	"context"
	"strings"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// A queue with no QueueName gets a CloudFormation-generated name. With a long
// stack name and logical ID that name must still fit SQS's 80-character limit,
// or 75 plus ".fifo" for a FIFO queue.
func TestCFNGeneratedQueueNameFitsSQSLimit(t *testing.T) {
	longStack := "my-really-long-application-stack-name-for-the-orders-service-prod"
	longLogical := "OrdersProcessingDeadLetterQueueForTheBillingPipeline"

	tests := []struct {
		name  string
		props map[string]any
		fifo  bool
	}{
		{name: "standard", props: map[string]any{}},
		{name: "fifo", props: map[string]any{"FifoQueue": true}, fifo: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := New()

			res, err := sqsQueueProvisioner{p.SQS}.Create(context.Background(), cfn.ResourceRequest{
				LogicalID: longLogical, Type: "AWS::SQS::Queue", Properties: tc.props, StackName: longStack,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			name := res.Attributes["QueueName"]
			if len(name) > 80 {
				t.Fatalf("generated name %q is %d characters, want at most 80", name, len(name))
			}

			if tc.fifo != strings.HasSuffix(name, ".fifo") {
				t.Fatalf("generated name %q: fifo=%v but .fifo suffix=%v", name, tc.fifo, !tc.fifo)
			}

			if !strings.HasPrefix(name, longStack[:10]) {
				t.Fatalf("generated name %q does not start with the stack name", name)
			}
		})
	}
}

// A short stack name and logical ID are kept whole, with a 12-character
// suffix. A FIFO queue with no QueueName still ends in ".fifo".
func TestCFNGeneratedQueueNameKeepsShortParts(t *testing.T) {
	p := New()

	res, err := sqsQueueProvisioner{p.SQS}.Create(context.Background(), cfn.ResourceRequest{
		LogicalID: "Queue", Type: "AWS::SQS::Queue", Properties: map[string]any{"FifoQueue": true}, StackName: "app",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	name := res.Attributes["QueueName"]
	if !strings.HasPrefix(name, "app-Queue-") || len(name) != len("app-Queue-")+12+len(".fifo") {
		t.Fatalf("generated name = %q, want app-Queue-<12 chars>.fifo", name)
	}
}
