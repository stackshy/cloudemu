package apprunner_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapprunner "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsapprunner.Client {
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

	return awsapprunner.NewFromConfig(cfg, func(o *awsapprunner.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createSvc(t *testing.T, c *awsapprunner.Client, name string) *artypes.Service {
	t.Helper()

	out, err := c.CreateService(context.Background(), &awsapprunner.CreateServiceInput{
		ServiceName: aws.String(name),
		SourceConfiguration: &artypes.SourceConfiguration{
			ImageRepository: &artypes.ImageRepository{
				ImageIdentifier:     aws.String("public.ecr.aws/aws-containers/hello-app-runner:latest"),
				ImageRepositoryType: artypes.ImageRepositoryTypeEcrPublic,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	return out.Service
}

func TestSDKServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	svc := createSvc(t, c, "web")
	if svc.Status != artypes.ServiceStatusRunning {
		t.Fatalf("status = %s, want RUNNING", svc.Status)
	}

	arn := aws.ToString(svc.ServiceArn)
	if !strings.Contains(arn, ":apprunner:") {
		t.Fatalf("unexpected ARN: %s", arn)
	}

	if aws.ToString(svc.ServiceUrl) == "" {
		t.Fatal("expected a synthesized ServiceUrl")
	}

	desc, err := c.DescribeService(ctx, &awsapprunner.DescribeServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeService: %v", err)
	}

	if desc.Service.SourceConfiguration == nil || desc.Service.SourceConfiguration.ImageRepository == nil {
		t.Fatal("SourceConfiguration did not round-trip")
	}

	if got := aws.ToString(desc.Service.SourceConfiguration.ImageRepository.ImageIdentifier); !strings.Contains(
		got, "hello-app-runner") {
		t.Fatalf("ImageIdentifier = %q", got)
	}

	if _, err := c.PauseService(ctx, &awsapprunner.PauseServiceInput{ServiceArn: aws.String(arn)}); err != nil {
		t.Fatalf("PauseService: %v", err)
	}

	resumed, err := c.ResumeService(ctx, &awsapprunner.ResumeServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ResumeService: %v", err)
	}

	if resumed.Service.Status != artypes.ServiceStatusRunning {
		t.Fatalf("after resume status = %s", resumed.Service.Status)
	}

	del, err := c.DeleteService(ctx, &awsapprunner.DeleteServiceInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if del.Service.Status != artypes.ServiceStatusDeleted {
		t.Fatalf("after delete status = %s, want DELETED", del.Service.Status)
	}
}

func TestSDKListServices(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	createSvc(t, c, "a")
	createSvc(t, c, "b")

	out, err := c.ListServices(ctx, &awsapprunner.ListServicesInput{})
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	if len(out.ServiceSummaryList) != 2 {
		t.Fatalf("want 2 services, got %d", len(out.ServiceSummaryList))
	}
}

func TestSDKAutoScalingConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	c1, err := c.CreateAutoScalingConfiguration(ctx, &awsapprunner.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: aws.String("hi"),
		MaxConcurrency:               aws.Int32(150),
		MaxSize:                      aws.Int32(20),
		MinSize:                      aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("CreateAutoScalingConfiguration: %v", err)
	}

	if aws.ToInt32(c1.AutoScalingConfiguration.AutoScalingConfigurationRevision) != 1 {
		t.Fatalf("revision = %d, want 1", aws.ToInt32(c1.AutoScalingConfiguration.AutoScalingConfigurationRevision))
	}

	c2, err := c.CreateAutoScalingConfiguration(ctx, &awsapprunner.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: aws.String("hi"),
	})
	if err != nil {
		t.Fatalf("CreateAutoScalingConfiguration r2: %v", err)
	}

	if aws.ToInt32(c2.AutoScalingConfiguration.AutoScalingConfigurationRevision) != 2 {
		t.Fatalf("revision = %d, want 2", aws.ToInt32(c2.AutoScalingConfiguration.AutoScalingConfigurationRevision))
	}

	arn := aws.ToString(c2.AutoScalingConfiguration.AutoScalingConfigurationArn)

	desc, err := c.DescribeAutoScalingConfiguration(ctx, &awsapprunner.DescribeAutoScalingConfigurationInput{
		AutoScalingConfigurationArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeAutoScalingConfiguration: %v", err)
	}

	if desc.AutoScalingConfiguration.Status != artypes.AutoScalingConfigurationStatusActive {
		t.Fatalf("status = %s, want ACTIVE", desc.AutoScalingConfiguration.Status)
	}

	list, err := c.ListAutoScalingConfigurations(ctx, &awsapprunner.ListAutoScalingConfigurationsInput{})
	if err != nil {
		t.Fatalf("ListAutoScalingConfigurations: %v", err)
	}

	if len(list.AutoScalingConfigurationSummaryList) != 2 {
		t.Fatalf("want 2 ASC revisions, got %d", len(list.AutoScalingConfigurationSummaryList))
	}
}

func TestSDKConnection(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	out, err := c.CreateConnection(ctx, &awsapprunner.CreateConnectionInput{
		ConnectionName: aws.String("gh"),
		ProviderType:   artypes.ProviderTypeGithub,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if out.Connection.ProviderType != artypes.ProviderTypeGithub {
		t.Fatalf("provider = %s", out.Connection.ProviderType)
	}

	list, err := c.ListConnections(ctx, &awsapprunner.ListConnectionsInput{})
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}

	if len(list.ConnectionSummaryList) != 1 {
		t.Fatalf("want 1 connection, got %d", len(list.ConnectionSummaryList))
	}
}

func TestSDKDescribeMissingServiceTypedError(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.DescribeService(ctx, &awsapprunner.DescribeServiceInput{
		ServiceArn: aws.String("arn:aws:apprunner:us-east-1:123456789012:service/missing/deadbeef"),
	})
	if err == nil {
		t.Fatal("expected error for missing service")
	}

	var nfe *artypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ResourceNotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestSDKObservabilityConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	out, err := c.CreateObservabilityConfiguration(ctx, &awsapprunner.CreateObservabilityConfigurationInput{
		ObservabilityConfigurationName: aws.String("obs"),
		TraceConfiguration:             &artypes.TraceConfiguration{Vendor: artypes.TracingVendorAwsxray},
	})
	if err != nil {
		t.Fatalf("CreateObservabilityConfiguration: %v", err)
	}

	oc := out.ObservabilityConfiguration
	if oc.Status != artypes.ObservabilityConfigurationStatusActive {
		t.Fatalf("status = %s, want ACTIVE", oc.Status)
	}

	if oc.TraceConfiguration == nil || oc.TraceConfiguration.Vendor != artypes.TracingVendorAwsxray {
		t.Fatal("TraceConfiguration did not round-trip")
	}

	list, err := c.ListObservabilityConfigurations(ctx, &awsapprunner.ListObservabilityConfigurationsInput{})
	if err != nil {
		t.Fatalf("ListObservabilityConfigurations: %v", err)
	}

	if len(list.ObservabilityConfigurationSummaryList) != 1 {
		t.Fatalf("want 1 obs config, got %d", len(list.ObservabilityConfigurationSummaryList))
	}
}

func TestSDKVpcConnector(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	out, err := c.CreateVpcConnector(ctx, &awsapprunner.CreateVpcConnectorInput{
		VpcConnectorName: aws.String("vc"),
		Subnets:          []string{"subnet-1", "subnet-2"},
		SecurityGroups:   []string{"sg-1"},
	})
	if err != nil {
		t.Fatalf("CreateVpcConnector: %v", err)
	}

	if out.VpcConnector.Status != artypes.VpcConnectorStatusActive {
		t.Fatalf("status = %s", out.VpcConnector.Status)
	}

	if len(out.VpcConnector.Subnets) != 2 {
		t.Fatalf("subnets = %v", out.VpcConnector.Subnets)
	}

	list, err := c.ListVpcConnectors(ctx, &awsapprunner.ListVpcConnectorsInput{})
	if err != nil || len(list.VpcConnectors) != 1 {
		t.Fatalf("ListVpcConnectors: n=%d err=%v", len(list.VpcConnectors), err)
	}
}

func TestSDKVpcIngressConnection(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	svc := createSvc(t, c, "ingress")
	arn := aws.ToString(svc.ServiceArn)

	out, err := c.CreateVpcIngressConnection(ctx, &awsapprunner.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: aws.String("vic"),
		ServiceArn:               aws.String(arn),
		IngressVpcConfiguration: &artypes.IngressVpcConfiguration{
			VpcId: aws.String("vpc-1"), VpcEndpointId: aws.String("vpce-1"),
		},
	})
	if err != nil {
		t.Fatalf("CreateVpcIngressConnection: %v", err)
	}

	vicArn := aws.ToString(out.VpcIngressConnection.VpcIngressConnectionArn)
	if out.VpcIngressConnection.Status != artypes.VpcIngressConnectionStatusAvailable {
		t.Fatalf("status = %s", out.VpcIngressConnection.Status)
	}

	list, err := c.ListVpcIngressConnections(ctx, &awsapprunner.ListVpcIngressConnectionsInput{
		Filter: &artypes.ListVpcIngressConnectionsFilter{ServiceArn: aws.String(arn)},
	})
	if err != nil || len(list.VpcIngressConnectionSummaryList) != 1 {
		t.Fatalf("ListVpcIngressConnections: n=%d err=%v", len(list.VpcIngressConnectionSummaryList), err)
	}

	upd, err := c.UpdateVpcIngressConnection(ctx, &awsapprunner.UpdateVpcIngressConnectionInput{
		VpcIngressConnectionArn: aws.String(vicArn),
		IngressVpcConfiguration: &artypes.IngressVpcConfiguration{
			VpcId: aws.String("vpc-2"), VpcEndpointId: aws.String("vpce-2"),
		},
	})
	if err != nil {
		t.Fatalf("UpdateVpcIngressConnection: %v", err)
	}

	if aws.ToString(upd.VpcIngressConnection.IngressVpcConfiguration.VpcEndpointId) != "vpce-2" {
		t.Fatal("update did not round-trip the new endpoint id")
	}

	del, err := c.DeleteVpcIngressConnection(ctx, &awsapprunner.DeleteVpcIngressConnectionInput{
		VpcIngressConnectionArn: aws.String(vicArn),
	})
	if err != nil || del.VpcIngressConnection.Status != artypes.VpcIngressConnectionStatusDeleted {
		t.Fatalf("DeleteVpcIngressConnection: %v", err)
	}
}

func TestSDKCustomDomain(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	svc := createSvc(t, c, "cd")
	arn := aws.ToString(svc.ServiceArn)

	assoc, err := c.AssociateCustomDomain(ctx, &awsapprunner.AssociateCustomDomainInput{
		ServiceArn: aws.String(arn), DomainName: aws.String("example.com"),
	})
	if err != nil {
		t.Fatalf("AssociateCustomDomain: %v", err)
	}

	if aws.ToString(assoc.DNSTarget) == "" || assoc.CustomDomain == nil {
		t.Fatal("expected DNSTarget and CustomDomain")
	}

	desc, err := c.DescribeCustomDomains(ctx, &awsapprunner.DescribeCustomDomainsInput{ServiceArn: aws.String(arn)})
	if err != nil || len(desc.CustomDomains) != 1 {
		t.Fatalf("DescribeCustomDomains: n=%d err=%v", len(desc.CustomDomains), err)
	}

	if _, err := c.DisassociateCustomDomain(ctx, &awsapprunner.DisassociateCustomDomainInput{
		ServiceArn: aws.String(arn), DomainName: aws.String("example.com"),
	}); err != nil {
		t.Fatalf("DisassociateCustomDomain: %v", err)
	}
}

func TestSDKListOperations(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	svc := createSvc(t, c, "ops")
	arn := aws.ToString(svc.ServiceArn)

	if _, err := c.PauseService(ctx, &awsapprunner.PauseServiceInput{ServiceArn: aws.String(arn)}); err != nil {
		t.Fatalf("PauseService: %v", err)
	}

	out, err := c.ListOperations(ctx, &awsapprunner.ListOperationsInput{ServiceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}

	if len(out.OperationSummaryList) != 2 {
		t.Fatalf("want 2 operations (CREATE + PAUSE), got %d", len(out.OperationSummaryList))
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	svc := createSvc(t, c, "tagged")
	arn := aws.ToString(svc.ServiceArn)

	if _, err := c.TagResource(ctx, &awsapprunner.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []artypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	list, err := c.ListTagsForResource(ctx, &awsapprunner.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || len(list.Tags) != 1 || aws.ToString(list.Tags[0].Value) != "prod" {
		t.Fatalf("ListTagsForResource: %v err=%v", list.Tags, err)
	}

	if _, err := c.UntagResource(ctx, &awsapprunner.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	list, _ = c.ListTagsForResource(ctx, &awsapprunner.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if len(list.Tags) != 0 {
		t.Fatalf("after untag want 0 tags, got %d", len(list.Tags))
	}
}

func TestSDKPauseIllegalStateTypedError(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	svc := createSvc(t, c, "svc")
	arn := aws.ToString(svc.ServiceArn)

	if _, err := c.PauseService(ctx, &awsapprunner.PauseServiceInput{ServiceArn: aws.String(arn)}); err != nil {
		t.Fatalf("PauseService: %v", err)
	}

	_, err := c.PauseService(ctx, &awsapprunner.PauseServiceInput{ServiceArn: aws.String(arn)})
	if err == nil {
		t.Fatal("expected InvalidStateException pausing a PAUSED service")
	}

	var ise *artypes.InvalidStateException
	if !errors.As(err, &ise) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want InvalidStateException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want InvalidStateException, got %v", err)
	}
}
