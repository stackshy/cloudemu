package cloudformation_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go"
)

const conditionalTemplate = `Parameters:
  Env:
    Type: String
    AllowedValues: [prod, dev]
Conditions:
  IsProd: !Equals [!Ref Env, prod]
Resources:
  Data:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${AWS::StackName}-data"
  Audit:
    Type: AWS::SQS::Queue
    Condition: IsProd
    Properties:
      QueueName: !Sub "${AWS::StackName}-audit"
Outputs:
  Topics:
    Value: !Join [",", !Ref "AWS::NotificationARNs"]
`

func stackResourceIDs(t *testing.T, c clients, stack string) string {
	t.Helper()

	out, err := c.cfn.DescribeStackResources(context.Background(),
		&awscfn.DescribeStackResourcesInput{StackName: aws.String(stack)})
	if err != nil {
		t.Fatalf("DescribeStackResources: %v", err)
	}

	ids := make([]string, 0, len(out.StackResources))
	for _, r := range out.StackResources {
		ids = append(ids, aws.ToString(r.LogicalResourceId))
	}

	sort.Strings(ids)

	return strings.Join(ids, ",")
}

func TestConditionalStacksRealSDK(t *testing.T) {
	c := boot(t)
	ctx := context.Background()
	topics := []string{"arn:aws:sns:us-east-1:000000000000:t1"}

	for _, env := range []string{"prod", "dev"} {
		_, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
			StackName: aws.String(env), TemplateBody: aws.String(conditionalTemplate), NotificationARNs: topics,
			Parameters: []cfntypes.Parameter{{ParameterKey: aws.String("Env"), ParameterValue: aws.String(env)}},
		})
		if err != nil {
			t.Fatalf("CreateStack %s: %v", env, err)
		}
	}

	if got := stackResourceIDs(t, c, "prod"); got != "Audit,Data" {
		t.Fatalf("prod resources = %s", got)
	}

	if got := stackResourceIDs(t, c, "dev"); got != "Data" {
		t.Fatalf("dev resources = %s", got)
	}

	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("dev")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	st := desc.Stacks[0]
	if len(st.NotificationARNs) != 1 || st.NotificationARNs[0] != topics[0] || outputMap(st.Outputs)["Topics"] != topics[0] {
		t.Fatalf("topics = %v outputs = %v", st.NotificationARNs, outputMap(st.Outputs))
	}

	_, err = c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("qa"), TemplateBody: aws.String(conditionalTemplate),
		Parameters: []cfntypes.Parameter{{ParameterKey: aws.String("Env"), ParameterValue: aws.String("qa")}},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationError" ||
		apiErr.ErrorMessage() != "Parameter 'Env' must be one of AllowedValues" {
		t.Fatalf("bad AllowedValues err = %v", err)
	}
}

func TestSSMParameterTypeRealSDK(t *testing.T) {
	c := boot(t)
	ctx := context.Background()

	seed := `Resources:
  P:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /app/queue
      Type: String
      Value: from-ssm
`
	if _, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("seed"), TemplateBody: aws.String(seed),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := `Parameters:
  Name:
    Type: AWS::SSM::Parameter::Value<String>
    Default: /app/queue
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Ref Name
`
	if _, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("app"), TemplateBody: aws.String(body),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("app")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	p := desc.Stacks[0].Parameters[0]
	if aws.ToString(p.ParameterValue) != "/app/queue" || aws.ToString(p.ResolvedValue) != "from-ssm" {
		t.Fatalf("parameter = %s / %s", aws.ToString(p.ParameterValue), aws.ToString(p.ResolvedValue))
	}

	res, err := c.cfn.DescribeStackResources(ctx, &awscfn.DescribeStackResourcesInput{StackName: aws.String("app")})
	if err != nil || !strings.HasSuffix(aws.ToString(res.StackResources[0].PhysicalResourceId), "/from-ssm") {
		t.Fatalf("queue = %v %v", res, err)
	}
}
