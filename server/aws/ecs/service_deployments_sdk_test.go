package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestSDK_ServiceDeployments(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("web"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	for range 2 {
		if _, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
			Cluster: aws.String("prod"), Service: aws.String("web"), ForceNewDeployment: true,
		}); err != nil {
			t.Fatalf("UpdateService: %v", err)
		}
	}

	list, err := client.ListServiceDeployments(ctx, &awsecs.ListServiceDeploymentsInput{
		Cluster: aws.String("prod"), Service: aws.String("web"), MaxResults: aws.Int32(2),
	})
	if err != nil || len(list.ServiceDeployments) != 2 || list.NextToken == nil {
		t.Fatalf("ListServiceDeployments page 1: %v %+v", err, list)
	}

	rest, err := client.ListServiceDeployments(ctx, &awsecs.ListServiceDeploymentsInput{
		Cluster: aws.String("prod"), Service: aws.String("web"), MaxResults: aws.Int32(2), NextToken: list.NextToken,
	})
	if err != nil || len(rest.ServiceDeployments) != 1 || rest.NextToken != nil {
		t.Fatalf("ListServiceDeployments page 2: %v %+v", err, rest)
	}

	if st := list.ServiceDeployments[0].Status; st != ecstypes.ServiceDeploymentStatusSuccessful {
		t.Fatalf("status = %s", st)
	}

	filtered, err := client.ListServiceDeployments(ctx, &awsecs.ListServiceDeploymentsInput{
		Cluster: aws.String("prod"), Service: aws.String("web"), Status: []ecstypes.ServiceDeploymentStatus{ecstypes.ServiceDeploymentStatusStopped},
	})
	if err != nil || len(filtered.ServiceDeployments) != 0 {
		t.Fatalf("status filter: %v %+v", err, filtered)
	}

	arn := aws.ToString(list.ServiceDeployments[0].ServiceDeploymentArn)

	desc, err := client.DescribeServiceDeployments(ctx, &awsecs.DescribeServiceDeploymentsInput{
		ServiceDeploymentArns: []string{arn, "arn:aws:ecs:us-east-1:000000000000:service-deployment/prod/web/none"},
	})
	if err != nil || len(desc.ServiceDeployments) != 1 || len(desc.Failures) != 1 ||
		len(desc.ServiceDeployments[0].SourceServiceRevisions) != 1 {
		t.Fatalf("DescribeServiceDeployments: %v %+v", err, desc)
	}

	rev := desc.ServiceDeployments[0].TargetServiceRevision
	if aws.ToString(rev.Arn) != aws.ToString(list.ServiceDeployments[0].TargetServiceRevisionArn) {
		t.Fatalf("target revision mismatch: %+v", rev)
	}

	revs, err := client.DescribeServiceRevisions(ctx, &awsecs.DescribeServiceRevisionsInput{ServiceRevisionArns: []string{aws.ToString(rev.Arn)}})
	if err != nil || len(revs.ServiceRevisions) != 1 || aws.ToString(revs.ServiceRevisions[0].TaskDefinition) == "" {
		t.Fatalf("DescribeServiceRevisions: %v %+v", err, revs)
	}

	_, err = client.StopServiceDeployment(ctx, &awsecs.StopServiceDeploymentInput{
		ServiceDeploymentArn: aws.String(arn), StopType: ecstypes.StopServiceDeploymentStopTypeRollback,
	})

	var conflict *ecstypes.ConflictException
	if !errorsAs(err, &conflict) {
		t.Fatalf("stopping a completed deployment: want *ConflictException, got %T: %v", err, err)
	}

	_, err = client.StopServiceDeployment(ctx, &awsecs.StopServiceDeploymentInput{
		ServiceDeploymentArn: aws.String("arn:aws:ecs:us-east-1:000000000000:service-deployment/prod/web/none"),
		StopType:             ecstypes.StopServiceDeploymentStopTypeAbort,
	})

	var notFound *ecstypes.ServiceDeploymentNotFoundException
	if !errorsAs(err, &notFound) {
		t.Fatalf("want *ServiceDeploymentNotFoundException, got %T: %v", err, err)
	}

	// stopType is optional on the wire (an omitted value means ROLLBACK).
	_, err = client.StopServiceDeployment(ctx, &awsecs.StopServiceDeploymentInput{
		ServiceDeploymentArn: aws.String("arn:aws:ecs:us-east-1:000000000000:service-deployment/prod/web/none"),
	})
	if !errorsAs(err, &notFound) {
		t.Fatalf("no stopType, unknown ARN: want *ServiceDeploymentNotFoundException, got %T: %v", err, err)
	}

	_, err = client.StopServiceDeployment(ctx, &awsecs.StopServiceDeploymentInput{ServiceDeploymentArn: aws.String(arn)})
	if !errorsAs(err, &conflict) {
		t.Fatalf("no stopType, completed deployment: want *ConflictException, got %T: %v", err, err)
	}
}

func TestSDK_ServiceConnectNamespace(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	for _, name := range []string{"api", "web"} {
		if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
			Cluster: aws.String("prod"), ServiceName: aws.String(name), TaskDefinition: aws.String("fg"),
			LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
			ServiceConnectConfiguration: &ecstypes.ServiceConnectConfiguration{Enabled: true, Namespace: aws.String("mesh")},
		}); err != nil {
			t.Fatalf("CreateService %s: %v", name, err)
		}
	}

	got, err := client.ListServicesByNamespace(ctx, &awsecs.ListServicesByNamespaceInput{Namespace: aws.String("mesh"), MaxResults: aws.Int32(1)})
	if err != nil || len(got.ServiceArns) != 1 || got.NextToken == nil {
		t.Fatalf("ListServicesByNamespace page 1: %v %+v", err, got)
	}

	rest, err := client.ListServicesByNamespace(ctx, &awsecs.ListServicesByNamespaceInput{
		Namespace: aws.String("mesh"), MaxResults: aws.Int32(1), NextToken: got.NextToken,
	})
	if err != nil || len(rest.ServiceArns) != 1 || rest.NextToken != nil {
		t.Fatalf("ListServicesByNamespace page 2: %v %+v", err, rest)
	}

	desc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{Cluster: aws.String("prod"), Services: []string{"api"}})
	if err != nil || len(desc.Services[0].Deployments) == 0 || desc.Services[0].Deployments[0].ServiceConnectConfiguration == nil ||
		aws.ToString(desc.Services[0].Deployments[0].ServiceConnectConfiguration.Namespace) != "mesh" {
		t.Fatalf("deployment must echo serviceConnectConfiguration: %v", err)
	}

	empty, err := client.ListServicesByNamespace(ctx, &awsecs.ListServicesByNamespaceInput{Namespace: aws.String("nobody")})
	if err != nil || len(empty.ServiceArns) != 0 {
		t.Fatalf("unknown namespace must list nothing: %v %+v", err, empty)
	}

	_, err = client.ListServicesByNamespace(ctx, &awsecs.ListServicesByNamespaceInput{Namespace: aws.String("mesh"), MaxResults: aws.Int32(101)})

	var invalid *ecstypes.InvalidParameterException
	if !errorsAs(err, &invalid) {
		t.Fatalf("want *InvalidParameterException, got %T: %v", err, err)
	}
}
