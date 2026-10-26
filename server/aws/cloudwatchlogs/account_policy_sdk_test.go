package cloudwatchlogs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func TestSDKAccountPolicyLifecycle(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()

	put, err := c.PutAccountPolicy(ctx, &cwl.PutAccountPolicyInput{
		PolicyName: aws.String("acct-dp"), PolicyType: cwltypes.PolicyTypeDataProtectionPolicy,
		PolicyDocument: aws.String(sdkDPPolicy),
	})
	if err != nil {
		t.Fatalf("PutAccountPolicy: %v", err)
	}

	ap := put.AccountPolicy
	if ap.Scope != cwltypes.ScopeAll || aws.ToString(ap.AccountId) == "" || aws.ToInt64(ap.LastUpdatedTime) == 0 {
		t.Fatalf("accountPolicy = %+v", ap)
	}

	// The account policy masks every group, including one created later.
	mustLogGroupStream(t, c, "/acct/g", "s")
	mustSDK(t, "PutLogEvents", func() error {
		_, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName: aws.String("/acct/g"), LogStreamName: aws.String("s"),
			LogEvents: []cwltypes.InputLogEvent{{Timestamp: aws.Int64(time.Now().UnixMilli()), Message: aws.String("to a@b.io")}},
		})

		return err
	})

	ev, err := c.GetLogEvents(ctx, &cwl.GetLogEventsInput{LogGroupName: aws.String("/acct/g"), LogStreamName: aws.String("s")})
	if err != nil || aws.ToString(ev.Events[0].Message) != "to ******" {
		t.Fatalf("GetLogEvents = %v %+v", err, ev)
	}

	desc, err := c.DescribeAccountPolicies(ctx, &cwl.DescribeAccountPoliciesInput{
		PolicyType: cwltypes.PolicyTypeDataProtectionPolicy, PolicyName: aws.String("acct-dp"),
	})
	if err != nil || len(desc.AccountPolicies) != 1 || aws.ToString(desc.AccountPolicies[0].PolicyDocument) != sdkDPPolicy {
		t.Fatalf("DescribeAccountPolicies = %v %+v", err, desc)
	}

	var lee *cwltypes.LimitExceededException

	_, err = c.PutAccountPolicy(ctx, &cwl.PutAccountPolicyInput{
		PolicyName: aws.String("acct-dp2"), PolicyType: cwltypes.PolicyTypeDataProtectionPolicy,
		PolicyDocument: aws.String(sdkDPPolicy),
	})
	if !errors.As(err, &lee) {
		t.Fatalf("second data protection policy = %v, want LimitExceededException", err)
	}

	mustSDK(t, "DeleteAccountPolicy", func() error {
		_, err := c.DeleteAccountPolicy(ctx, &cwl.DeleteAccountPolicyInput{
			PolicyName: aws.String("acct-dp"), PolicyType: cwltypes.PolicyTypeDataProtectionPolicy,
		})

		return err
	})

	var rnf *cwltypes.ResourceNotFoundException

	_, err = c.DeleteAccountPolicy(ctx, &cwl.DeleteAccountPolicyInput{
		PolicyName: aws.String("acct-dp"), PolicyType: cwltypes.PolicyTypeDataProtectionPolicy,
	})
	if !errors.As(err, &rnf) {
		t.Fatalf("second delete = %v, want ResourceNotFoundException", err)
	}
}

func TestSDKAccountPolicyValidation(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()

	var ipe *cwltypes.InvalidParameterException

	_, err := c.PutAccountPolicy(ctx, &cwl.PutAccountPolicyInput{
		PolicyName: aws.String("aws/x"), PolicyType: cwltypes.PolicyTypeTransformerPolicy, PolicyDocument: aws.String("[]"),
	})
	if !errors.As(err, &ipe) {
		t.Fatalf("aws/ name = %v, want InvalidParameterException", err)
	}

	_, err = c.PutAccountPolicy(ctx, &cwl.PutAccountPolicyInput{
		PolicyName: aws.String("sub"), PolicyType: cwltypes.PolicyTypeSubscriptionFilterPolicy,
		PolicyDocument:    aws.String(`{"DestinationArn":"arn:aws:lambda:us-east-1:000000000000:function:f"}`),
		SelectionCriteria: aws.String(`LogGroupName IN ["a"]`),
	})
	if !errors.As(err, &ipe) {
		t.Fatalf("IN criteria on a subscription policy = %v, want InvalidParameterException", err)
	}

	_, err = c.DescribeAccountPolicies(ctx, &cwl.DescribeAccountPoliciesInput{PolicyType: "GLOBAL"})
	if !errors.As(err, &ipe) {
		t.Fatalf("describe with an unknown policyType = %v, want InvalidParameterException", err)
	}
}
