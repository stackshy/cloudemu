package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const iamCompatTrustPolicy = `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "ec2.amazonaws.com"},
    "Action": "sts:AssumeRole"
  }]
}`

const iamCompatPolicyDoc = `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["s3:ListBucket"],
    "Resource": "*"
  }]
}`

// TestCompatAWSIAM drives the real aws-sdk-go-v2 IAM client against CloudEmu's
// in-process wire server via the shared harness, recording one result per
// portable IAM operation the handler routes.
func TestCompatAWSIAM(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{
		IAM: cloud.IAM,
		// EC2 shares the AWS query protocol; wiring it exercises dispatch
		// precedence — the IAM handler must claim the body first.
		EC2: cloud.EC2,
	})

	client := iam.NewFromConfig(sess.Config(), func(o *iam.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		userName    = "alice"
		roleName    = "app-role"
		groupName   = "devs"
		policyName  = "list-bucket"
		profileName = "app-profile"
	)

	var (
		policyArn         string
		newVersionID      string
		nonDefaultVersion string
		accessKeyID       string
	)

	// Users.
	sess.Op("iam", "CreateUser", func() error {
		_, err := client.CreateUser(ctx, &iam.CreateUserInput{
			UserName: aws.String(userName),
			Tags:     []iamtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
		})
		return err
	})
	sess.Op("iam", "GetUser", func() error {
		_, err := client.GetUser(ctx, &iam.GetUserInput{UserName: aws.String(userName)})
		return err
	})
	sess.Op("iam", "ListUsers", func() error {
		_, err := client.ListUsers(ctx, &iam.ListUsersInput{})
		return err
	})

	// Roles.
	sess.Op("iam", "CreateRole", func() error {
		_, err := client.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 aws.String(roleName),
			AssumeRolePolicyDocument: aws.String(iamCompatTrustPolicy),
		})
		return err
	})
	sess.Op("iam", "GetRole", func() error {
		_, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
		return err
	})
	sess.Op("iam", "ListRoles", func() error {
		_, err := client.ListRoles(ctx, &iam.ListRolesInput{})
		return err
	})

	// Groups.
	sess.Op("iam", "CreateGroup", func() error {
		_, err := client.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String(groupName)})
		return err
	})
	sess.Op("iam", "GetGroup", func() error {
		_, err := client.GetGroup(ctx, &iam.GetGroupInput{GroupName: aws.String(groupName)})
		return err
	})
	sess.Op("iam", "ListGroups", func() error {
		_, err := client.ListGroups(ctx, &iam.ListGroupsInput{})
		return err
	})
	sess.Op("iam", "AddUserToGroup", func() error {
		_, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{
			GroupName: aws.String(groupName),
			UserName:  aws.String(userName),
		})
		return err
	})
	sess.Op("iam", "ListGroupsForUser", func() error {
		_, err := client.ListGroupsForUser(ctx, &iam.ListGroupsForUserInput{UserName: aws.String(userName)})
		return err
	})
	sess.Op("iam", "RemoveUserFromGroup", func() error {
		_, err := client.RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{
			GroupName: aws.String(groupName),
			UserName:  aws.String(userName),
		})
		return err
	})
	sess.Op("iam", "DeleteGroup", func() error {
		_, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String(groupName)})
		return err
	})

	// Managed policies.
	sess.Op("iam", "CreatePolicy", func() error {
		out, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
			PolicyName:     aws.String(policyName),
			PolicyDocument: aws.String(iamCompatPolicyDoc),
		})
		if err == nil {
			policyArn = aws.ToString(out.Policy.Arn)
		}
		return err
	})
	sess.Op("iam", "GetPolicy", func() error {
		_, err := client.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(policyArn)})
		return err
	})
	sess.Op("iam", "ListPolicies", func() error {
		_, err := client.ListPolicies(ctx, &iam.ListPoliciesInput{})
		return err
	})

	// Policy versions.
	sess.Op("iam", "CreatePolicyVersion", func() error {
		out, err := client.CreatePolicyVersion(ctx, &iam.CreatePolicyVersionInput{
			PolicyArn:      aws.String(policyArn),
			PolicyDocument: aws.String(iamCompatPolicyDoc),
			SetAsDefault:   true,
		})
		if err == nil {
			newVersionID = aws.ToString(out.PolicyVersion.VersionId)
		}
		return err
	})
	sess.Op("iam", "GetPolicyVersion", func() error {
		_, err := client.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
			PolicyArn: aws.String(policyArn),
			VersionId: aws.String(newVersionID),
		})
		return err
	})
	sess.Op("iam", "ListPolicyVersions", func() error {
		out, err := client.ListPolicyVersions(ctx, &iam.ListPolicyVersionsInput{PolicyArn: aws.String(policyArn)})
		if err == nil {
			for _, v := range out.Versions {
				if !v.IsDefaultVersion {
					nonDefaultVersion = aws.ToString(v.VersionId)
				}
			}
		}
		return err
	})
	sess.Op("iam", "SetDefaultPolicyVersion", func() error {
		_, err := client.SetDefaultPolicyVersion(ctx, &iam.SetDefaultPolicyVersionInput{
			PolicyArn: aws.String(policyArn),
			VersionId: aws.String(newVersionID),
		})
		return err
	})
	sess.Op("iam", "DeletePolicyVersion", func() error {
		_, err := client.DeletePolicyVersion(ctx, &iam.DeletePolicyVersionInput{
			PolicyArn: aws.String(policyArn),
			VersionId: aws.String(nonDefaultVersion),
		})
		return err
	})

	// User <-> policy attachments.
	sess.Op("iam", "AttachUserPolicy", func() error {
		_, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
			UserName:  aws.String(userName),
			PolicyArn: aws.String(policyArn),
		})
		return err
	})
	sess.Op("iam", "ListAttachedUserPolicies", func() error {
		_, err := client.ListAttachedUserPolicies(ctx, &iam.ListAttachedUserPoliciesInput{UserName: aws.String(userName)})
		return err
	})
	sess.Op("iam", "DetachUserPolicy", func() error {
		_, err := client.DetachUserPolicy(ctx, &iam.DetachUserPolicyInput{
			UserName:  aws.String(userName),
			PolicyArn: aws.String(policyArn),
		})
		return err
	})

	// Role <-> policy attachments.
	sess.Op("iam", "AttachRolePolicy", func() error {
		_, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyArn),
		})
		return err
	})
	sess.Op("iam", "ListAttachedRolePolicies", func() error {
		_, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(roleName)})
		return err
	})
	sess.Op("iam", "DetachRolePolicy", func() error {
		_, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyArn),
		})
		return err
	})

	// Instance profiles.
	sess.Op("iam", "CreateInstanceProfile", func() error {
		_, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
		})
		return err
	})
	sess.Op("iam", "GetInstanceProfile", func() error {
		_, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
		return err
	})
	sess.Op("iam", "ListInstanceProfiles", func() error {
		_, err := client.ListInstanceProfiles(ctx, &iam.ListInstanceProfilesInput{})
		return err
	})
	sess.Op("iam", "AddRoleToInstanceProfile", func() error {
		_, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
			RoleName:            aws.String(roleName),
		})
		return err
	})
	sess.Op("iam", "RemoveRoleFromInstanceProfile", func() error {
		_, err := client.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
			RoleName:            aws.String(roleName),
		})
		return err
	})
	sess.Op("iam", "DeleteInstanceProfile", func() error {
		_, err := client.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
		return err
	})

	// Access keys.
	sess.Op("iam", "CreateAccessKey", func() error {
		out, err := client.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String(userName)})
		if err == nil {
			accessKeyID = aws.ToString(out.AccessKey.AccessKeyId)
		}
		return err
	})
	sess.Op("iam", "ListAccessKeys", func() error {
		_, err := client.ListAccessKeys(ctx, &iam.ListAccessKeysInput{UserName: aws.String(userName)})
		return err
	})
	sess.Op("iam", "DeleteAccessKey", func() error {
		_, err := client.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
			UserName:    aws.String(userName),
			AccessKeyId: aws.String(accessKeyID),
		})
		return err
	})

	// Teardown of the shared resources, each a routed op in its own right.
	sess.Op("iam", "DeletePolicy", func() error {
		_, err := client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: aws.String(policyArn)})
		return err
	})
	sess.Op("iam", "DeleteRole", func() error {
		_, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)})
		return err
	})
	sess.Op("iam", "DeleteUser", func() error {
		_, err := client.DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String(userName)})
		return err
	})
}
