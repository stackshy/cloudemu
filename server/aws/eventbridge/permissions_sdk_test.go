package eventbridge_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// TestSDKEventBridgePutPermissionReflectedOnDescribe drives PutPermission with a
// legacy trio + Condition through the real SDK and verifies DescribeEventBus
// surfaces the resulting resource policy.
func TestSDKEventBridgePutPermissionReflectedOnDescribe(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	_, err := client.PutPermission(ctx, &awseb.PutPermissionInput{
		Action:      aws.String("events:PutEvents"),
		Principal:   aws.String("123456789012"),
		StatementId: aws.String("cross-acct"),
		Condition: &ebtypes.Condition{
			Type:  aws.String("StringEquals"),
			Key:   aws.String("aws:PrincipalOrgID"),
			Value: aws.String("o-abc123"),
		},
	})
	if err != nil {
		t.Fatalf("PutPermission: %v", err)
	}

	out, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	if out.Policy == nil {
		t.Fatal("DescribeEventBus returned no Policy")
	}

	for _, want := range []string{"cross-acct", "events:PutEvents", "123456789012", "aws:PrincipalOrgID", "o-abc123"} {
		if !strings.Contains(*out.Policy, want) {
			t.Fatalf("policy missing %q: %s", want, *out.Policy)
		}
	}
}

func TestSDKEventBridgeRemovePermission(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	for _, sid := range []string{"s1", "s2"} {
		if _, err := client.PutPermission(ctx, &awseb.PutPermissionInput{
			Action:      aws.String("events:PutEvents"),
			Principal:   aws.String("*"),
			StatementId: aws.String(sid),
		}); err != nil {
			t.Fatalf("PutPermission(%s): %v", sid, err)
		}
	}

	if _, err := client.RemovePermission(ctx, &awseb.RemovePermissionInput{StatementId: aws.String("s1")}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}

	out, err := client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	if out.Policy == nil || strings.Contains(*out.Policy, `"s1"`) || !strings.Contains(*out.Policy, `"s2"`) {
		t.Fatalf("unexpected policy after removal: %v", aws.ToString(out.Policy))
	}

	// unknown statement id -> ResourceNotFoundException
	_, err = client.RemovePermission(ctx, &awseb.RemovePermissionInput{StatementId: aws.String("ghost")})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("want ResourceNotFoundException, got %T: %v", err, err)
	}

	// RemoveAllPermissions clears the policy entirely.
	if _, err := client.RemovePermission(ctx, &awseb.RemovePermissionInput{
		RemoveAllPermissions: true,
	}); err != nil {
		t.Fatalf("RemovePermission(all): %v", err)
	}

	out, err = client.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	if aws.ToString(out.Policy) != "" {
		t.Fatalf("policy not cleared: %q", aws.ToString(out.Policy))
	}
}

func TestSDKEventBridgeListRuleNamesByTarget(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	const targetArn = "arn:aws:sqs:us-east-1:123456789012:orders"

	// Two rules point at targetArn; one points elsewhere.
	rules := map[string]string{
		"rule-a": targetArn,
		"rule-b": targetArn,
		"rule-c": "arn:aws:sqs:us-east-1:123456789012:other",
	}

	for name, arn := range rules {
		if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
			Name:         aws.String(name),
			EventPattern: aws.String(`{"source":["app"]}`),
		}); err != nil {
			t.Fatalf("PutRule(%s): %v", name, err)
		}

		if _, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
			Rule:    aws.String(name),
			Targets: []ebtypes.Target{{Id: aws.String("t1"), Arn: aws.String(arn)}},
		}); err != nil {
			t.Fatalf("PutTargets(%s): %v", name, err)
		}
	}

	out, err := client.ListRuleNamesByTarget(ctx, &awseb.ListRuleNamesByTargetInput{
		TargetArn: aws.String(targetArn),
	})
	if err != nil {
		t.Fatalf("ListRuleNamesByTarget: %v", err)
	}

	got := out.RuleNames
	sort.Strings(got)

	if len(got) != 2 || got[0] != "rule-a" || got[1] != "rule-b" {
		t.Fatalf("want [rule-a rule-b], got %v", got)
	}

	// Pagination: Limit=1 returns one name plus a NextToken that resumes.
	page1, err := client.ListRuleNamesByTarget(ctx, &awseb.ListRuleNamesByTargetInput{
		TargetArn: aws.String(targetArn),
		Limit:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("ListRuleNamesByTarget page1: %v", err)
	}

	if len(page1.RuleNames) != 1 || page1.NextToken == nil {
		t.Fatalf("want 1 name + NextToken, got names=%v token=%v", page1.RuleNames, page1.NextToken)
	}

	page2, err := client.ListRuleNamesByTarget(ctx, &awseb.ListRuleNamesByTargetInput{
		TargetArn: aws.String(targetArn),
		Limit:     aws.Int32(1),
		NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListRuleNamesByTarget page2: %v", err)
	}

	if len(page2.RuleNames) != 1 || page2.RuleNames[0] == page1.RuleNames[0] {
		t.Fatalf("page2 should return the other name, got %v (page1 %v)", page2.RuleNames, page1.RuleNames)
	}
}
