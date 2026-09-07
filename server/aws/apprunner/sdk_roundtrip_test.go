package apprunner_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsar "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsar.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{AppRunner: cloud.AppRunner})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsar.NewFromConfig(cfg, func(o *awsar.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func sampleSource() *artypes.SourceConfiguration {
	return &artypes.SourceConfiguration{
		ImageRepository: &artypes.ImageRepository{
			ImageIdentifier:     aws.String("123456789012.dkr.ecr.us-east-1.amazonaws.com/app:latest"),
			ImageRepositoryType: artypes.ImageRepositoryTypeEcr,
			ImageConfiguration:  &artypes.ImageConfiguration{Port: aws.String("8080")},
		},
		AutoDeploymentsEnabled: aws.Bool(false),
	}
}

func mustCreate(t *testing.T, c *awsar.Client) *artypes.Service {
	t.Helper()

	out, err := c.CreateService(context.Background(), &awsar.CreateServiceInput{
		ServiceName:         aws.String("my-app"),
		SourceConfiguration: sampleSource(),
		InstanceConfiguration: &artypes.InstanceConfiguration{
			Cpu: aws.String("1024"), Memory: aws.String("2048"),
		},
		Tags: []artypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	return out.Service
}

func TestSDKServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	svc := mustCreate(t, c)

	// A service is RUNNING synchronously so an IaC waiter never hangs.
	if svc.Status != artypes.ServiceStatusRunning {
		t.Fatalf("create status = %q, want RUNNING", svc.Status)
	}

	arn := aws.ToString(svc.ServiceArn)
	id := aws.ToString(svc.ServiceId)
	wantArn := "arn:aws:apprunner:us-east-1:123456789012:service/my-app/" + id
	if arn != wantArn {
		t.Fatalf("arn = %q, want %q", arn, wantArn)
	}

	if aws.ToString(svc.ServiceUrl) != id+".us-east-1.awsapprunner.com" {
		t.Fatalf("url = %q drifted", aws.ToString(svc.ServiceUrl))
	}

	// Computed fields are byte-stable across a Describe read.
	desc, err := c.DescribeService(ctx, &awsar.DescribeServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeService: %v", err)
	}

	d := desc.Service
	if aws.ToString(d.ServiceArn) != arn || aws.ToString(d.ServiceUrl) != aws.ToString(svc.ServiceUrl) ||
		aws.ToString(d.ServiceId) != id || !d.CreatedAt.Equal(*svc.CreatedAt) {
		t.Fatalf("computed fields drifted across reads: %+v", d)
	}

	if d.SourceConfiguration.ImageRepository == nil ||
		aws.ToString(d.SourceConfiguration.ImageRepository.ImageConfiguration.Port) != "8080" {
		t.Fatalf("source configuration did not round-trip: %+v", d.SourceConfiguration)
	}

	list, err := c.ListServices(ctx, &awsar.ListServicesInput{})
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	if len(list.ServiceSummaryList) != 1 || aws.ToString(list.ServiceSummaryList[0].ServiceId) != id {
		t.Fatalf("list = %d services, want 1 with id %q", len(list.ServiceSummaryList), id)
	}

	del, err := c.DeleteService(ctx, &awsar.DeleteServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if del.Service.Status != artypes.ServiceStatusDeleted {
		t.Fatalf("delete status = %q, want DELETED", del.Service.Status)
	}

	_, err = c.DescribeService(ctx, &awsar.DescribeServiceInput{ServiceArn: aws.String(arn)})

	var notFound *artypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("describe after delete err = %v, want ResourceNotFoundException", err)
	}
}

func TestSDKPauseResumeStartDeploymentStateMachine(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	svc := mustCreate(t, c)
	arn := aws.ToString(svc.ServiceArn)

	pause, err := c.PauseService(ctx, &awsar.PauseServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("PauseService: %v", err)
	}

	if pause.Service.Status != artypes.ServiceStatusPaused || aws.ToString(pause.OperationId) == "" {
		t.Fatalf("pause: status=%q op=%q", pause.Service.Status, aws.ToString(pause.OperationId))
	}

	desc, err := c.DescribeService(ctx, &awsar.DescribeServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeService: %v", err)
	}

	if desc.Service.Status != artypes.ServiceStatusPaused {
		t.Fatalf("describe after pause = %q, want PAUSED", desc.Service.Status)
	}

	resume, err := c.ResumeService(ctx, &awsar.ResumeServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ResumeService: %v", err)
	}

	if resume.Service.Status != artypes.ServiceStatusRunning {
		t.Fatalf("resume status = %q, want RUNNING", resume.Service.Status)
	}

	deploy, err := c.StartDeployment(ctx, &awsar.StartDeploymentInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("StartDeployment: %v", err)
	}

	if aws.ToString(deploy.OperationId) == "" {
		t.Fatalf("expected a deployment OperationId")
	}

	ops, err := c.ListOperations(ctx, &awsar.ListOperationsInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}

	// CREATE_SERVICE + PAUSE + RESUME + START_DEPLOYMENT, newest first.
	if len(ops.OperationSummaryList) != 4 || ops.OperationSummaryList[0].Type != artypes.OperationTypeStartDeployment {
		t.Fatalf("operations = %+v, want START_DEPLOYMENT newest", ops.OperationSummaryList)
	}
}

func TestSDKIllegalTransitionRejected(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	svc := mustCreate(t, c)
	arn := aws.ToString(svc.ServiceArn)

	if _, err := c.PauseService(ctx, &awsar.PauseServiceInput{ServiceArn: aws.String(arn)}); err != nil {
		t.Fatalf("first PauseService: %v", err)
	}

	// Pausing an already-PAUSED service is an InvalidStateException.
	_, err := c.PauseService(ctx, &awsar.PauseServiceInput{ServiceArn: aws.String(arn)})

	var invalidState *artypes.InvalidStateException
	if !errors.As(err, &invalidState) {
		t.Fatalf("double pause err = %v, want InvalidStateException", err)
	}
}

func TestSDKAutoScalingAndTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateAutoScalingConfiguration(ctx, &awsar.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: aws.String("high-availability"),
		MinSize:                      aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("CreateAutoScalingConfiguration: %v", err)
	}

	cfg := create.AutoScalingConfiguration
	if aws.ToInt32(cfg.AutoScalingConfigurationRevision) != 1 || !aws.ToBool(cfg.Latest) || aws.ToInt32(cfg.MinSize) != 5 {
		t.Fatalf("auto scaling config wrong: %+v", cfg)
	}

	arn := aws.ToString(cfg.AutoScalingConfigurationArn)
	if _, err := c.TagResource(ctx, &awsar.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []artypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsar.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "team" {
		t.Fatalf("tags = %+v, want team=platform", tags.Tags)
	}
}
