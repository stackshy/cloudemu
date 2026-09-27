package cloudformation_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	psdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func bootWithProvider(t *testing.T) (*awscfn.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	return awscfn.NewFromConfig(cfg, func(o *awscfn.Options) { o.BaseEndpoint = aws.String(ts.URL) }), cloud
}

func describeStack(t *testing.T, c *awscfn.Client, name string) cfntypes.Stack {
	t.Helper()

	out, err := c.DescribeStacks(context.Background(), &awscfn.DescribeStacksInput{StackName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	return out.Stacks[0]
}

func apiErrorCode(t *testing.T, err error) (code, msg string) {
	t.Helper()

	var ae smithy.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want an API error, got %v", err)
	}

	return ae.ErrorCode(), ae.ErrorMessage()
}

const ssmQueueTemplate = `Parameters:
  Timeout:
    Type: AWS::SSM::Parameter::Value<String>
    Default: /cfg/timeout
Resources:
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: ssm-driven
      VisibilityTimeout: !Ref Timeout
`

// A changed Parameter Store value updates the queue in place on the next
// UpdateStack, and an unchanged stack gets the real "No updates" error.
func TestSSMParameterChangeUpdatesQueueRealSDK(t *testing.T) {
	ctx := context.Background()
	c, cloud := bootWithProvider(t)

	put := func(v string) {
		if _, _, err := cloud.SSM.PutParameter(ctx, psdriver.PutConfig{
			Name: "/cfg/timeout", Value: v, Type: "String", Overwrite: true,
		}); err != nil {
			t.Fatalf("PutParameter: %v", err)
		}
	}

	put("40")

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("q"), TemplateBody: aws.String(ssmQueueTemplate),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	url := aws.ToString(describeResource(t, c, "q").PhysicalResourceId)
	update := &awscfn.UpdateStackInput{
		StackName: aws.String("q"), UsePreviousTemplate: aws.Bool(true),
		Parameters: []cfntypes.Parameter{{ParameterKey: aws.String("Timeout"), UsePreviousValue: aws.Bool(true)}},
	}

	_, err := c.UpdateStack(ctx, update)
	if code, msg := apiErrorCode(t, err); code != "ValidationError" || msg != "No updates are to be performed." {
		t.Fatalf("unchanged update: %s %s", code, msg)
	}

	put("90")

	if _, err = c.UpdateStack(ctx, update); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}

	st := describeStack(t, c, "q")
	if st.StackStatus != cfntypes.StackStatusUpdateComplete {
		t.Fatalf("status = %s", st.StackStatus)
	}

	if got := aws.ToString(describeResource(t, c, "q").PhysicalResourceId); got != url {
		t.Fatalf("queue replaced: %s -> %s", url, got)
	}

	attrs, err := cloud.SQS.GetQueueAttributes(ctx, url)
	if err != nil || attrs.VisibilityTimeout != 90 {
		t.Fatalf("VisibilityTimeout not updated: %+v %v", attrs, err)
	}
}

func describeResource(t *testing.T, c *awscfn.Client, stack string) cfntypes.StackResource {
	t.Helper()

	out, err := c.DescribeStackResources(context.Background(), &awscfn.DescribeStackResourcesInput{StackName: aws.String(stack)})
	if err != nil || len(out.StackResources) == 0 {
		t.Fatalf("DescribeStackResources: %v", err)
	}

	return out.StackResources[0]
}

const paramStackV1 = `Resources:
  Old:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /app/p
      Type: String
      Value: v1
      Tier: Standard
`

// paramStackV2 moves Old to the Advanced tier in place, then fails on an
// unsupported type. Parameter Store refuses to move it back to Standard, so
// the rollback fails.
const paramStackV2 = `Resources:
  Old:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /app/p
      Type: String
      Value: v1
      Tier: Advanced
  Bad:
    Type: AWS::Unknown::Thing
    DependsOn: Old
`

func TestUpdateRollbackFailedRealSDK(t *testing.T) {
	ctx := context.Background()

	for _, skip := range [][]string{nil, {"Old"}} {
		c, cloud := bootWithProvider(t)

		if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
			StackName: aws.String("p"), TemplateBody: aws.String(paramStackV1),
		}); err != nil {
			t.Fatalf("CreateStack: %v", err)
		}

		if _, err := c.UpdateStack(ctx, &awscfn.UpdateStackInput{
			StackName: aws.String("p"), TemplateBody: aws.String(paramStackV2),
		}); err != nil {
			t.Fatalf("UpdateStack: %v", err)
		}

		st := describeStack(t, c, "p")
		if st.StackStatus != cfntypes.StackStatusUpdateRollbackFailed ||
			!strings.HasPrefix(aws.ToString(st.StackStatusReason), "The following resource(s) failed to update: [Old].") {
			t.Fatalf("status = %s (%s)", st.StackStatus, aws.ToString(st.StackStatusReason))
		}

		_, err := c.UpdateStack(ctx, &awscfn.UpdateStackInput{StackName: aws.String("p"), UsePreviousTemplate: aws.Bool(true)})
		if code, _ := apiErrorCode(t, err); code != "ValidationError" {
			t.Fatalf("update in UPDATE_ROLLBACK_FAILED: %v", err)
		}

		// Without a skip, the cause is fixed first: the Advanced parameter is
		// deleted so the rollback can put back a Standard one.
		if skip == nil {
			if err = cloud.SSM.DeleteParameter(ctx, "/app/p"); err != nil {
				t.Fatalf("DeleteParameter: %v", err)
			}
		}

		if _, err = c.ContinueUpdateRollback(ctx, &awscfn.ContinueUpdateRollbackInput{
			StackName: aws.String("p"), ResourcesToSkip: skip,
		}); err != nil {
			t.Fatalf("ContinueUpdateRollback: %v", err)
		}

		if got := describeStack(t, c, "p").StackStatus; got != cfntypes.StackStatusUpdateRollbackComplete {
			t.Fatalf("after continue: %s", got)
		}

		if _, getErr := cloud.SSM.GetParameter(ctx, "/app/p", false); getErr != nil {
			t.Fatalf("skip=%v: /app/p must exist after the rollback: %v", skip, getErr)
		}

		_, err = c.ContinueUpdateRollback(ctx, &awscfn.ContinueUpdateRollbackInput{StackName: aws.String("p")})
		if code, _ := apiErrorCode(t, err); code != "ValidationError" {
			t.Fatalf("continue from UPDATE_ROLLBACK_COMPLETE: %v", err)
		}
	}
}

func TestInsufficientCapabilitiesRealSDK(t *testing.T) {
	c, _ := bootWithProvider(t)

	body := `Resources:
  Role:
    Type: AWS::IAM::Role
    Properties:
      RoleName: named-role
      AssumeRolePolicyDocument: {Version: "2012-10-17", Statement: []}
`

	_, err := c.CreateStack(context.Background(), &awscfn.CreateStackInput{
		StackName: aws.String("iam"), TemplateBody: aws.String(body), Capabilities: []cfntypes.Capability{cfntypes.CapabilityCapabilityIam},
	})

	var ice *cfntypes.InsufficientCapabilitiesException
	if !errors.As(err, &ice) || aws.ToString(ice.Message) != "Requires capabilities : [CAPABILITY_NAMED_IAM]" {
		t.Fatalf("want InsufficientCapabilitiesException, got %v", err)
	}

	if _, err = c.CreateStack(context.Background(), &awscfn.CreateStackInput{
		StackName: aws.String("iam"), TemplateBody: aws.String(body),
		Capabilities: []cfntypes.Capability{cfntypes.CapabilityCapabilityNamedIam},
	}); err != nil {
		t.Fatalf("CreateStack with NAMED_IAM: %v", err)
	}
}
