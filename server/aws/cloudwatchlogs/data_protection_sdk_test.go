package cloudwatchlogs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

const sdkDPPolicy = `{"Name":"p","Version":"2021-06-01","Statement":[
{"Sid":"audit","DataIdentifier":["arn:aws:dataprotection::aws:data-identifier/EmailAddress",
"arn:aws:dataprotection::aws:data-identifier/AwsSecretKey"],"Operation":{"Audit":{"FindingsDestination":{}}}},
{"Sid":"redact","DataIdentifier":["arn:aws:dataprotection::aws:data-identifier/EmailAddress",
"arn:aws:dataprotection::aws:data-identifier/AwsSecretKey"],"Operation":{"Deidentify":{"MaskConfig":{}}}}]}`

func TestSDKDataProtectionMaskAndUnmask(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g, s := "/dp/g", "s"

	mustLogGroupStream(t, c, g, s)

	put, err := c.PutDataProtectionPolicy(ctx, &cwl.PutDataProtectionPolicyInput{
		LogGroupIdentifier: aws.String(g), PolicyDocument: aws.String(sdkDPPolicy),
	})
	if err != nil {
		t.Fatalf("PutDataProtectionPolicy: %v", err)
	}

	if aws.ToString(put.LogGroupIdentifier) != g || aws.ToInt64(put.LastUpdatedTime) == 0 {
		t.Fatalf("put response = %+v", put)
	}

	msg := "user a@b.io key aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	mustSDK(t, "PutLogEvents", func() error {
		_, err := c.PutLogEvents(ctx, &cwl.PutLogEventsInput{
			LogGroupName: aws.String(g), LogStreamName: aws.String(s),
			LogEvents: []cwltypes.InputLogEvent{{Timestamp: aws.Int64(time.Now().UnixMilli()), Message: aws.String(msg)}},
		})

		return err
	})

	want := "user ****** key aws_secret_access_key=" + strings.Repeat("*", 40)

	for _, unmask := range []bool{false, true} {
		ev, err := c.GetLogEvents(ctx, &cwl.GetLogEventsInput{
			LogGroupName: aws.String(g), LogStreamName: aws.String(s), Unmask: unmask,
		})
		if err != nil || len(ev.Events) != 1 {
			t.Fatalf("GetLogEvents unmask=%v: %v %+v", unmask, err, ev)
		}

		fl, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(g), Unmask: unmask})
		if err != nil || len(fl.Events) != 1 {
			t.Fatalf("FilterLogEvents unmask=%v: %v %+v", unmask, err, fl)
		}

		exp := want
		if unmask {
			exp = msg
		}

		if got := aws.ToString(ev.Events[0].Message); got != exp {
			t.Fatalf("GetLogEvents unmask=%v = %q, want %q", unmask, got, exp)
		}

		if got := aws.ToString(fl.Events[0].Message); got != exp {
			t.Fatalf("FilterLogEvents unmask=%v = %q, want %q", unmask, got, exp)
		}
	}

	groups, err := c.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{LogGroupNamePrefix: aws.String(g)})
	if err != nil || groups.LogGroups[0].DataProtectionStatus != cwltypes.DataProtectionStatusActivated {
		t.Fatalf("DescribeLogGroups dataProtectionStatus: %v %+v", err, groups)
	}
}

func TestSDKDataProtectionPolicyLifecycle(t *testing.T) {
	c := newLogsClient(t)
	ctx := context.Background()
	g := "/dp/life"

	mustLogGroupStream(t, c, g, "s")

	got, err := c.GetDataProtectionPolicy(ctx, &cwl.GetDataProtectionPolicyInput{LogGroupIdentifier: aws.String(g)})
	if err != nil || got.PolicyDocument != nil {
		t.Fatalf("Get before put = %v, %+v; want no document", err, got)
	}

	arn := "arn:aws:logs:us-east-1:000000000000:log-group:" + g
	mustSDK(t, "PutDataProtectionPolicy", func() error {
		_, err := c.PutDataProtectionPolicy(ctx, &cwl.PutDataProtectionPolicyInput{
			LogGroupIdentifier: aws.String(arn), PolicyDocument: aws.String(sdkDPPolicy),
		})

		return err
	})

	got, err = c.GetDataProtectionPolicy(ctx, &cwl.GetDataProtectionPolicyInput{LogGroupIdentifier: aws.String(g)})
	if err != nil || aws.ToString(got.PolicyDocument) != sdkDPPolicy {
		t.Fatalf("Get after put = %v, %+v", err, got)
	}

	mustSDK(t, "DeleteDataProtectionPolicy", func() error {
		_, err := c.DeleteDataProtectionPolicy(ctx, &cwl.DeleteDataProtectionPolicyInput{LogGroupIdentifier: aws.String(g)})
		return err
	})

	var rnf *cwltypes.ResourceNotFoundException

	_, err = c.DeleteDataProtectionPolicy(ctx, &cwl.DeleteDataProtectionPolicyInput{LogGroupIdentifier: aws.String(g)})
	if !errors.As(err, &rnf) {
		t.Fatalf("second delete = %v, want ResourceNotFoundException", err)
	}

	_, err = c.GetDataProtectionPolicy(ctx, &cwl.GetDataProtectionPolicyInput{LogGroupIdentifier: aws.String("/dp/none")})
	if !errors.As(err, &rnf) {
		t.Fatalf("missing group = %v, want ResourceNotFoundException", err)
	}

	var ipe *cwltypes.InvalidParameterException

	_, err = c.PutDataProtectionPolicy(ctx, &cwl.PutDataProtectionPolicyInput{
		LogGroupIdentifier: aws.String(g), PolicyDocument: aws.String(`{"Statement":[]}`),
	})
	if !errors.As(err, &ipe) {
		t.Fatalf("bad document = %v, want InvalidParameterException", err)
	}
}
