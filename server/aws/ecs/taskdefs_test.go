package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// TestSDKTaskDefinitionCompatibilities guards the derived compatibilities and
// requiresAttributes fields real ECS returns on a registered task definition
// (distinct from the caller's requiresCompatibilities). An EC2 task def with
// role ARNs must advertise EC2 compatibility and the docker-remote-api,
// task-iam-role, and execution-role-ecr-pull required attributes; a Fargate-only
// awsvpc def must advertise both FARGATE and EC2 compatibility and no attributes.
func TestSDKTaskDefinitionCompatibilities(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	reg, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("ec2app"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("app"), Image: aws.String("app:latest"), Memory: aws.Int32(256),
		}},
		TaskRoleArn:      aws.String("arn:aws:iam::123456789012:role/task"),
		ExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/exec"),
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	td := reg.TaskDefinition

	if !hasCompatibility(td.Compatibilities, ecstypes.CompatibilityEc2) {
		t.Fatalf("compatibilities = %v, want EC2 present", td.Compatibilities)
	}

	if !hasRequiredAttr(td.RequiresAttributes, "com.amazonaws.ecs.capability.docker-remote-api.1.18") {
		t.Fatalf("requiresAttributes = %v, want docker-remote-api", td.RequiresAttributes)
	}

	if !hasRequiredAttr(td.RequiresAttributes, "com.amazonaws.ecs.capability.task-iam-role") {
		t.Fatalf("requiresAttributes = %v, want task-iam-role (TaskRoleArn set)", td.RequiresAttributes)
	}

	if !hasRequiredAttr(td.RequiresAttributes, "ecs.capability.execution-role-ecr-pull") {
		t.Fatalf("requiresAttributes = %v, want execution-role-ecr-pull (ExecutionRoleArn set)", td.RequiresAttributes)
	}

	// A Fargate-only awsvpc def: FARGATE (derived) and EC2 (always) compatible,
	// and no requiresAttributes.
	fgReg, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("fgapp"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("app"), Image: aws.String("app:latest"),
		}},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition (fargate): %v", err)
	}

	fg := fgReg.TaskDefinition

	if !hasCompatibility(fg.Compatibilities, ecstypes.CompatibilityFargate) {
		t.Fatalf("fargate compatibilities = %v, want FARGATE present", fg.Compatibilities)
	}

	if len(fg.RequiresAttributes) != 0 {
		t.Fatalf("fargate-only requiresAttributes = %v, want none", fg.RequiresAttributes)
	}

	// The derived fields must survive DescribeTaskDefinition too.
	desc, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String("ec2app"),
	})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition: %v", err)
	}

	if !hasCompatibility(desc.TaskDefinition.Compatibilities, ecstypes.CompatibilityEc2) {
		t.Fatalf("describe compatibilities = %v, want EC2", desc.TaskDefinition.Compatibilities)
	}
}

func hasCompatibility(list []ecstypes.Compatibility, want ecstypes.Compatibility) bool {
	for _, c := range list {
		if c == want {
			return true
		}
	}

	return false
}

func hasRequiredAttr(list []ecstypes.Attribute, name string) bool {
	for i := range list {
		if aws.ToString(list[i].Name) == name {
			return true
		}
	}

	return false
}

// TestSDKDeregisterTaskDefinitionRequiresRevision guards that DeregisterTaskDefinition
// rejects a bare family name. Real ECS requires a specific revision (family:revision
// or a full ARN) and will not pick one on the caller's behalf, returning a
// ClientException; cloudemu previously deregistered the latest ACTIVE revision
// silently.
func TestSDKDeregisterTaskDefinitionRequiresRevision(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	registerNginx(t, client, ctx)

	_, err := client.DeregisterTaskDefinition(ctx, &awsecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String("web"),
	})
	if err == nil {
		t.Fatalf("DeregisterTaskDefinition(bare family) = nil error, want ClientException")
	}

	var ce *ecstypes.ClientException
	if !errorsAs(err, &ce) {
		t.Fatalf("DeregisterTaskDefinition error = %T (%v), want *ClientException", err, err)
	}

	// The named revision must remain ACTIVE — nothing was deregistered.
	desc, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String("web:1"),
	})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition(web:1): %v", err)
	}

	if desc.TaskDefinition.Status != ecstypes.TaskDefinitionStatusActive {
		t.Fatalf("web:1 status = %v, want ACTIVE (untouched)", desc.TaskDefinition.Status)
	}

	// A specific revision still deregisters normally.
	dereg, err := client.DeregisterTaskDefinition(ctx, &awsecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String("web:1"),
	})
	if err != nil {
		t.Fatalf("DeregisterTaskDefinition(web:1): %v", err)
	}

	if dereg.TaskDefinition.Status != ecstypes.TaskDefinitionStatusInactive {
		t.Fatalf("web:1 status = %v, want INACTIVE after deregister", dereg.TaskDefinition.Status)
	}
}
