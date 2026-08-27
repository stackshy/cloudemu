package eventbridge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// Real EventBridge refuses to delete a rule that still has targets: the caller
// must RemoveTargets first, otherwise DeleteRule returns a ValidationException.
func TestSDKEventBridgeDeleteRuleWithTargetsRejected(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name:         aws.String("has-targets"),
		EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("has-targets"),
		Targets: []ebtypes.Target{
			{Id: aws.String("t1"), Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:q")},
		},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	_, err := client.DeleteRule(ctx, &awseb.DeleteRuleInput{Name: aws.String("has-targets")})
	if err == nil {
		t.Fatal("DeleteRule with targets present should fail, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("want ValidationException, got %T: %v", err, err)
	}

	// The rule must still exist after the rejected delete.
	if _, err := client.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("has-targets")}); err != nil {
		t.Fatalf("rule should survive rejected DeleteRule: %v", err)
	}

	// After removing targets, DeleteRule succeeds.
	if _, err := client.RemoveTargets(ctx, &awseb.RemoveTargetsInput{
		Rule: aws.String("has-targets"),
		Ids:  []string{"t1"},
	}); err != nil {
		t.Fatalf("RemoveTargets: %v", err)
	}

	if _, err := client.DeleteRule(ctx, &awseb.DeleteRuleInput{Name: aws.String("has-targets")}); err != nil {
		t.Fatalf("DeleteRule after RemoveTargets: %v", err)
	}
}
