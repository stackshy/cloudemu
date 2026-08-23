package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// TestSDKInstanceProfilePath proves the instance profile Path is populated
// (previously empty).
func TestSDKInstanceProfilePath(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("web-profile"),
	}); err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	got, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String("web-profile"),
	})
	if err != nil {
		t.Fatalf("GetInstanceProfile: %v", err)
	}

	if path := aws.ToString(got.InstanceProfile.Path); path != "/" {
		t.Fatalf("InstanceProfile.Path = %q, want %q", path, "/")
	}
}

// TestSDKListInstanceProfilesForRole proves the operation returns the profiles
// that reference the role (previously always empty).
func TestSDKListInstanceProfilesForRole(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("app-role"),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("app-profile"),
	}); err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	if _, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("app-profile"), RoleName: aws.String("app-role"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}

	got, err := client.ListInstanceProfilesForRole(ctx, &iam.ListInstanceProfilesForRoleInput{
		RoleName: aws.String("app-role"),
	})
	if err != nil {
		t.Fatalf("ListInstanceProfilesForRole: %v", err)
	}

	if len(got.InstanceProfiles) != 1 {
		t.Fatalf("ListInstanceProfilesForRole = %d, want 1", len(got.InstanceProfiles))
	}

	if name := aws.ToString(got.InstanceProfiles[0].InstanceProfileName); name != "app-profile" {
		t.Fatalf("returned profile %q, want app-profile", name)
	}
}
