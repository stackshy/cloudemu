package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// TestCreateLaunchTemplateEnrichedResponse pins that CreateLaunchTemplate echoes
// the default/latest version numbers, a createdBy ARN, and resource tags, and
// that the KeyName inside LaunchTemplateData survives the round-trip.
func TestCreateLaunchTemplateEnrichedResponse(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	out, err := client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String("web"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			ImageId:      aws.String("ami-123"),
			InstanceType: ec2types.InstanceTypeT2Micro,
			KeyName:      aws.String("my-key"),
		},
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeLaunchTemplate,
			Tags:         []ec2types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateLaunchTemplate: %v", err)
	}

	lt := out.LaunchTemplate
	if got := aws.ToInt64(lt.DefaultVersionNumber); got != 1 {
		t.Errorf("DefaultVersionNumber = %d, want 1", got)
	}
	if got := aws.ToInt64(lt.LatestVersionNumber); got != 1 {
		t.Errorf("LatestVersionNumber = %d, want 1", got)
	}
	if aws.ToString(lt.CreatedBy) == "" {
		t.Errorf("CreatedBy is empty, want an ARN")
	}

	var env string
	for _, tg := range lt.Tags {
		if aws.ToString(tg.Key) == "env" {
			env = aws.ToString(tg.Value)
		}
	}
	if env != "prod" {
		t.Errorf("tag env = %q, want prod", env)
	}
}

// TestCreateLaunchTemplateVersionRoundTrip pins that a new version increments the
// number, inherits from SourceVersion, and shows up in DescribeLaunchTemplateVersions
// with correct default-version selection.
func TestCreateLaunchTemplateVersionRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String("app"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			ImageId:      aws.String("ami-base"),
			InstanceType: ec2types.InstanceTypeT2Micro,
			KeyName:      aws.String("kp"),
		},
	}); err != nil {
		t.Fatalf("CreateLaunchTemplate: %v", err)
	}

	ver, err := client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateName: aws.String("app"),
		SourceVersion:      aws.String("1"),
		VersionDescription: aws.String("bigger box"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			InstanceType: ec2types.InstanceTypeC5Large,
		},
	})
	if err != nil {
		t.Fatalf("CreateLaunchTemplateVersion: %v", err)
	}

	ltv := ver.LaunchTemplateVersion
	if got := aws.ToInt64(ltv.VersionNumber); got != 2 {
		t.Fatalf("VersionNumber = %d, want 2", got)
	}
	if aws.ToBool(ltv.DefaultVersion) {
		t.Errorf("version 2 DefaultVersion = true, want false")
	}
	// Instance type overwritten; image + key inherited from v1.
	if ltv.LaunchTemplateData == nil {
		t.Fatalf("LaunchTemplateData is nil")
	}
	if got := ltv.LaunchTemplateData.InstanceType; got != ec2types.InstanceTypeC5Large {
		t.Errorf("InstanceType = %q, want c5.large", got)
	}
	if got := aws.ToString(ltv.LaunchTemplateData.ImageId); got != "ami-base" {
		t.Errorf("inherited ImageId = %q, want ami-base", got)
	}
	if got := aws.ToString(ltv.LaunchTemplateData.KeyName); got != "kp" {
		t.Errorf("inherited KeyName = %q, want kp", got)
	}

	desc, err := client.DescribeLaunchTemplateVersions(ctx, &ec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateName: aws.String("app"),
	})
	if err != nil {
		t.Fatalf("DescribeLaunchTemplateVersions: %v", err)
	}
	if len(desc.LaunchTemplateVersions) != 2 {
		t.Fatalf("got %d versions, want 2", len(desc.LaunchTemplateVersions))
	}

	var defaults int
	for _, v := range desc.LaunchTemplateVersions {
		if aws.ToBool(v.DefaultVersion) {
			defaults++
			if aws.ToInt64(v.VersionNumber) != 1 {
				t.Errorf("default version = %d, want 1", aws.ToInt64(v.VersionNumber))
			}
		}
	}
	if defaults != 1 {
		t.Errorf("got %d default versions, want exactly 1", defaults)
	}
}

// TestGetLaunchTemplateDataFromRunningInstance pins that GetLaunchTemplateData
// synthesizes data from a running instance.
func TestGetLaunchTemplateDataFromRunningInstance(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-run"),
		InstanceType: ec2types.InstanceTypeT3Medium,
		KeyName:      aws.String("kp"),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := aws.ToString(run.Instances[0].InstanceId)

	data, err := client.GetLaunchTemplateData(ctx, &ec2.GetLaunchTemplateDataInput{
		InstanceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("GetLaunchTemplateData: %v", err)
	}
	if data.LaunchTemplateData == nil {
		t.Fatalf("LaunchTemplateData is nil")
	}
	if got := aws.ToString(data.LaunchTemplateData.ImageId); got != "ami-run" {
		t.Errorf("ImageId = %q, want ami-run", got)
	}
	if got := data.LaunchTemplateData.InstanceType; got != ec2types.InstanceTypeT3Medium {
		t.Errorf("InstanceType = %q, want t3.medium", got)
	}
	if got := aws.ToString(data.LaunchTemplateData.KeyName); got != "kp" {
		t.Errorf("KeyName = %q, want kp", got)
	}
}

// TestRunInstancesFromLaunchTemplate pins that RunInstances given a
// LaunchTemplate reference resolves the template's default version and applies
// its data (ImageId/InstanceType) to the launched instance — previously the
// template was silently ignored and ImageId came back empty.
func TestRunInstancesFromLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String("audit-lt"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			ImageId:      aws.String("ami-55555"),
			InstanceType: ec2types.InstanceTypeT3Small,
		},
	}); err != nil {
		t.Fatalf("CreateLaunchTemplate: %v", err)
	}

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		LaunchTemplate: &ec2types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String("audit-lt"),
		},
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(out.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(out.Instances))
	}
	if got := aws.ToString(out.Instances[0].ImageId); got != "ami-55555" {
		t.Errorf("ImageId = %q, want ami-55555 (launch template applied)", got)
	}
	if got := out.Instances[0].InstanceType; got != ec2types.InstanceTypeT3Small {
		t.Errorf("InstanceType = %q, want t3.small", got)
	}
}

// TestModifyLaunchTemplateSetsDefaultVersion pins that ModifyLaunchTemplate
// promotes a version to the default and that a subsequent RunInstances with no
// explicit version resolves the new default's data.
func TestModifyLaunchTemplateSetsDefaultVersion(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String("audit-lt"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{ImageId: aws.String("ami-55555")},
	}); err != nil {
		t.Fatalf("CreateLaunchTemplate: %v", err)
	}

	if _, err := client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateName: aws.String("audit-lt"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{ImageId: aws.String("ami-66666")},
	}); err != nil {
		t.Fatalf("CreateLaunchTemplateVersion: %v", err)
	}

	mod, err := client.ModifyLaunchTemplate(ctx, &ec2.ModifyLaunchTemplateInput{
		LaunchTemplateName: aws.String("audit-lt"),
		DefaultVersion:     aws.String("2"),
	})
	if err != nil {
		t.Fatalf("ModifyLaunchTemplate: %v", err)
	}
	if got := aws.ToInt64(mod.LaunchTemplate.DefaultVersionNumber); got != 2 {
		t.Fatalf("DefaultVersionNumber = %d, want 2", got)
	}

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		LaunchTemplate: &ec2types.LaunchTemplateSpecification{LaunchTemplateName: aws.String("audit-lt")},
		MinCount:       aws.Int32(1),
		MaxCount:       aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if got := aws.ToString(out.Instances[0].ImageId); got != "ami-66666" {
		t.Errorf("ImageId = %q, want ami-66666 (new default version)", got)
	}
}

// TestCreateDuplicateLaunchTemplateErrors pins the EC2-specific
// InvalidLaunchTemplateName.AlreadyExistsException code for a duplicate name.
func TestCreateDuplicateLaunchTemplateErrors(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	in := &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String("dup"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{ImageId: aws.String("ami-1")},
	}
	if _, err := client.CreateLaunchTemplate(ctx, in); err != nil {
		t.Fatalf("first CreateLaunchTemplate: %v", err)
	}

	_, err := client.CreateLaunchTemplate(ctx, in)
	if err == nil {
		t.Fatalf("duplicate CreateLaunchTemplate returned no error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidLaunchTemplateName.AlreadyExistsException" {
		t.Fatalf("error code = %v, want InvalidLaunchTemplateName.AlreadyExistsException", err)
	}
}
