package eventbridge_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// TestSDKEventBridgeErrorMessageNoInternalPrefix verifies wire error messages
// carry only the human text, without the cloudemu-internal "<Code>: " prefix.
// The DeleteRule-with-targets message must arrive as the verbatim AWS wording.
func TestSDKEventBridgeErrorMessageNoInternalPrefix(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("has-targets"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("has-targets"),
		Targets: []ebtypes.Target{{Id: aws.String("t1"), Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:q")}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	_, err := client.DeleteRule(ctx, &awseb.DeleteRuleInput{Name: aws.String("has-targets")})
	if err == nil {
		t.Fatal("DeleteRule with targets should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %T: %v", err, err)
	}

	msg := apiErr.ErrorMessage()

	if strings.HasPrefix(msg, "InvalidArgument:") {
		t.Fatalf("error message leaks internal code prefix: %q", msg)
	}

	if msg != "Rule can't be deleted since it has targets." {
		t.Fatalf("message = %q, want the verbatim AWS wording", msg)
	}
}
