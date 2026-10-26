package sts_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKAssumedRoleIDUsesRoleID checks that AssumedRoleId is the assumed
// role's own RoleId plus the session name, as real STS returns it, so two
// roles with the same session name get different ids.
func TestSDKAssumedRoleIDUsesRoleID(t *testing.T) {
	ts, iamClient := newTrustServer(t)
	ctx := context.Background()
	client := stsClient(t, ts.URL)

	for _, name := range []string{"role-a", "role-b"} {
		role, err := iamClient.CreateRole(ctx, &awsiam.CreateRoleInput{
			RoleName:                 aws.String(name),
			AssumeRolePolicyDocument: aws.String(allowRootTrust),
		})
		if err != nil {
			t.Fatalf("CreateRole %s: %v", name, err)
		}

		out, err := client.AssumeRole(ctx, &awssts.AssumeRoleInput{
			RoleArn:         aws.String("arn:aws:iam::" + testAccountID + ":role/" + name),
			RoleSessionName: aws.String("shared-session"),
		})
		if err != nil {
			t.Fatalf("AssumeRole %s: %v", name, err)
		}

		want := aws.ToString(role.Role.RoleId) + ":shared-session"
		if got := aws.ToString(out.AssumedRoleUser.AssumedRoleId); got != want {
			t.Fatalf("AssumedRoleId for %s = %q, want %q", name, got, want)
		}

		if !strings.HasPrefix(want, "AROA") {
			t.Fatalf("RoleId %q does not have the AROA prefix", want)
		}
	}
}

// TestSDKAssumedRoleIDWithoutIAM keeps the synthetic prefix when no IAM driver
// knows the role.
func TestSDKAssumedRoleIDWithoutIAM(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{STS: true, AccountID: testAccountID, Region: testRegion})

	out, err := stsClient(t, ts.URL).AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + testAccountID + ":role/anything"),
		RoleSessionName: aws.String("s"),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	if got := aws.ToString(out.AssumedRoleUser.AssumedRoleId); got != "AROACLOUDEMU0000000000:s" {
		t.Fatalf("AssumedRoleId = %q, want the synthetic fallback", got)
	}
}
