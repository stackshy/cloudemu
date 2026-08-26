package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	smithy "github.com/aws/smithy-go"
)

// createInstanceProfile creates an IAM role + instance profile pair and returns
// the profile's ARN and id, so the association tests can reference a real,
// resolvable profile exactly as launch-time does.
func createInstanceProfile(t *testing.T, iamc *iam.Client, name string) (arn, id string) {
	t.Helper()

	ctx := context.Background()
	roleName := name + "-role"

	if _, err := iamc.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[]}`),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	profile, err := iamc.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	if _, err := iamc.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(name),
		RoleName:            aws.String(roleName),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}

	return aws.ToString(profile.InstanceProfile.Arn), aws.ToString(profile.InstanceProfile.InstanceProfileId)
}

// runBareInstance launches one instance with no IAM profile and returns its id.
func runBareInstance(t *testing.T, ec2c *ec2.Client) string {
	t.Helper()

	run, err := ec2c.RunInstances(context.Background(), &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	return aws.ToString(run.Instances[0].InstanceId)
}

func instanceProfileARN(t *testing.T, ec2c *ec2.Client, id string) string {
	t.Helper()

	desc, err := ec2c.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	inst := desc.Reservations[0].Instances[0]
	if inst.IamInstanceProfile == nil {
		return ""
	}

	return aws.ToString(inst.IamInstanceProfile.Arn)
}

// TestAssociateIamInstanceProfileReflectedOnDescribe drives the real-user flow:
// launch an instance with no profile, attach one post-launch, then confirm both
// DescribeInstances (Arn + Id) and DescribeIamInstanceProfileAssociations reflect
// the attachment.
func TestAssociateIamInstanceProfileReflectedOnDescribe(t *testing.T) {
	ctx := context.Background()
	ec2c, iamc := newEC2AndIAMClients(t)

	wantARN, wantID := createInstanceProfile(t, iamc, "attach-profile")
	instanceID := runBareInstance(t, ec2c)

	if arn := instanceProfileARN(t, ec2c, instanceID); arn != "" {
		t.Fatalf("bare instance already has a profile %q, want none", arn)
	}

	assoc, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId: aws.String(instanceID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{
			Name: aws.String("attach-profile"),
		},
	})
	if err != nil {
		t.Fatalf("AssociateIamInstanceProfile: %v", err)
	}

	got := assoc.IamInstanceProfileAssociation
	if got == nil {
		t.Fatal("AssociateIamInstanceProfile returned no association")
	}

	associationID := aws.ToString(got.AssociationId)
	if associationID == "" {
		t.Fatal("AssociateIamInstanceProfile returned an empty association id")
	}

	if arn := aws.ToString(got.IamInstanceProfile.Arn); arn != wantARN {
		t.Fatalf("association IamInstanceProfile.Arn = %q, want %q", arn, wantARN)
	}

	// DescribeInstances must now reflect the profile ARN and id.
	if arn := instanceProfileARN(t, ec2c, instanceID); arn != wantARN {
		t.Fatalf("DescribeInstances IamInstanceProfile.Arn = %q, want %q", arn, wantARN)
	}

	desc, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if gotID := aws.ToString(desc.Reservations[0].Instances[0].IamInstanceProfile.Id); gotID != wantID {
		t.Fatalf("DescribeInstances IamInstanceProfile.Id = %q, want %q", gotID, wantID)
	}

	// DescribeIamInstanceProfileAssociations must list it as associated.
	list, err := ec2c.DescribeIamInstanceProfileAssociations(ctx,
		&ec2.DescribeIamInstanceProfileAssociationsInput{AssociationIds: []string{associationID}})
	if err != nil {
		t.Fatalf("DescribeIamInstanceProfileAssociations: %v", err)
	}

	if len(list.IamInstanceProfileAssociations) != 1 {
		t.Fatalf("DescribeIamInstanceProfileAssociations returned %d, want 1",
			len(list.IamInstanceProfileAssociations))
	}

	listed := list.IamInstanceProfileAssociations[0]
	if aws.ToString(listed.InstanceId) != instanceID {
		t.Fatalf("association InstanceId = %q, want %q", aws.ToString(listed.InstanceId), instanceID)
	}

	if listed.State != ec2types.IamInstanceProfileAssociationStateAssociated {
		t.Fatalf("association state = %q, want associated", listed.State)
	}
}

// TestAssociateIamInstanceProfileAlreadyAssociated confirms real EC2's rejection
// of a second association on an instance that already has one.
func TestAssociateIamInstanceProfileAlreadyAssociated(t *testing.T) {
	ctx := context.Background()
	ec2c, iamc := newEC2AndIAMClients(t)

	createInstanceProfile(t, iamc, "first-profile")
	createInstanceProfile(t, iamc, "second-profile")
	instanceID := runBareInstance(t, ec2c)

	if _, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String(instanceID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("first-profile")},
	}); err != nil {
		t.Fatalf("first AssociateIamInstanceProfile: %v", err)
	}

	_, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String(instanceID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("second-profile")},
	})
	if err == nil {
		t.Fatal("second AssociateIamInstanceProfile succeeded, want IncorrectState")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an API error", err)
	}

	if apiErr.ErrorCode() != "IncorrectState" {
		t.Fatalf("error code = %q, want IncorrectState", apiErr.ErrorCode())
	}
}

// TestReplaceIamInstanceProfileAssociation swaps the profile on an existing
// association and confirms the instance reflects the new profile.
func TestReplaceIamInstanceProfileAssociation(t *testing.T) {
	ctx := context.Background()
	ec2c, iamc := newEC2AndIAMClients(t)

	createInstanceProfile(t, iamc, "old-profile")
	newARN, newID := createInstanceProfile(t, iamc, "new-profile")
	instanceID := runBareInstance(t, ec2c)

	assoc, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String(instanceID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("old-profile")},
	})
	if err != nil {
		t.Fatalf("AssociateIamInstanceProfile: %v", err)
	}

	associationID := aws.ToString(assoc.IamInstanceProfileAssociation.AssociationId)

	replaced, err := ec2c.ReplaceIamInstanceProfileAssociation(ctx, &ec2.ReplaceIamInstanceProfileAssociationInput{
		AssociationId:      aws.String(associationID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("new-profile")},
	})
	if err != nil {
		t.Fatalf("ReplaceIamInstanceProfileAssociation: %v", err)
	}

	if arn := aws.ToString(replaced.IamInstanceProfileAssociation.IamInstanceProfile.Arn); arn != newARN {
		t.Fatalf("replaced association Arn = %q, want %q", arn, newARN)
	}

	if arn := instanceProfileARN(t, ec2c, instanceID); arn != newARN {
		t.Fatalf("DescribeInstances IamInstanceProfile.Arn = %q, want %q (new profile)", arn, newARN)
	}

	desc, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	if gotID := aws.ToString(desc.Reservations[0].Instances[0].IamInstanceProfile.Id); gotID != newID {
		t.Fatalf("DescribeInstances IamInstanceProfile.Id = %q, want %q", gotID, newID)
	}
}

// TestDisassociateIamInstanceProfile removes an association and confirms the
// instance's profile is cleared and the association no longer lists.
func TestDisassociateIamInstanceProfile(t *testing.T) {
	ctx := context.Background()
	ec2c, iamc := newEC2AndIAMClients(t)

	createInstanceProfile(t, iamc, "detach-profile")
	instanceID := runBareInstance(t, ec2c)

	assoc, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String(instanceID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("detach-profile")},
	})
	if err != nil {
		t.Fatalf("AssociateIamInstanceProfile: %v", err)
	}

	associationID := aws.ToString(assoc.IamInstanceProfileAssociation.AssociationId)

	if _, err := ec2c.DisassociateIamInstanceProfile(ctx, &ec2.DisassociateIamInstanceProfileInput{
		AssociationId: aws.String(associationID),
	}); err != nil {
		t.Fatalf("DisassociateIamInstanceProfile: %v", err)
	}

	if arn := instanceProfileARN(t, ec2c, instanceID); arn != "" {
		t.Fatalf("DescribeInstances still reports profile %q after disassociate, want none", arn)
	}

	list, err := ec2c.DescribeIamInstanceProfileAssociations(ctx,
		&ec2.DescribeIamInstanceProfileAssociationsInput{AssociationIds: []string{associationID}})
	if err != nil {
		t.Fatalf("DescribeIamInstanceProfileAssociations: %v", err)
	}

	if len(list.IamInstanceProfileAssociations) != 0 {
		t.Fatalf("association still listed after disassociate: got %d", len(list.IamInstanceProfileAssociations))
	}
}

// TestAssociateIamInstanceProfileMissingInstance confirms attaching to an
// instance that does not exist answers InvalidInstanceID.NotFound.
func TestAssociateIamInstanceProfileMissingInstance(t *testing.T) {
	ctx := context.Background()
	ec2c, iamc := newEC2AndIAMClients(t)

	createInstanceProfile(t, iamc, "orphan-profile")

	_, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String("i-doesnotexist"),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("orphan-profile")},
	})
	if err == nil {
		t.Fatal("AssociateIamInstanceProfile to a missing instance succeeded, want InvalidInstanceID.NotFound")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an API error", err)
	}

	if apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
		t.Fatalf("error code = %q, want InvalidInstanceID.NotFound", apiErr.ErrorCode())
	}
}

// TestAssociateIamInstanceProfileInvalidProfile confirms an unresolvable profile
// reference is rejected exactly as launch-time is (InvalidParameterValue), and no
// association is created.
func TestAssociateIamInstanceProfileInvalidProfile(t *testing.T) {
	ctx := context.Background()
	ec2c, _ := newEC2AndIAMClients(t)

	instanceID := runBareInstance(t, ec2c)

	_, err := ec2c.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String(instanceID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String("never-created")},
	})
	if err == nil {
		t.Fatal("AssociateIamInstanceProfile with a nonexistent profile succeeded, want InvalidParameterValue")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an API error", err)
	}

	if apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("error code = %q, want InvalidParameterValue", apiErr.ErrorCode())
	}

	// Nothing should have been recorded.
	if arn := instanceProfileARN(t, ec2c, instanceID); arn != "" {
		t.Fatalf("instance reflects profile %q after a failed associate, want none", arn)
	}
}
