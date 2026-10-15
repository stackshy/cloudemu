package apprunner_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsar "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
)

// TestSDKConnectionsConnectorsObservabilityAndTags covers the remaining wire
// operations: connections, VPC connector and observability describe/list, code
// repository sources, UpdateService and UntagResource.
func TestSDKConnectionsConnectorsObservabilityAndTags(t *testing.T) {
	ctx := context.Background()
	c, cloud := newClientCloud(t)
	subnet := seedSubnet(t, cloud)

	conn, err := c.CreateConnection(ctx, &awsar.CreateConnectionInput{
		ConnectionName: aws.String("gh-conn"), ProviderType: artypes.ProviderTypeGithub,
	})
	if err != nil || conn.Connection.Status != artypes.ConnectionStatusPendingHandshake {
		t.Fatalf("CreateConnection: %v %+v", err, conn)
	}

	conns, err := c.ListConnections(ctx, &awsar.ListConnectionsInput{})
	if err != nil || len(conns.ConnectionSummaryList) != 1 {
		t.Fatalf("ListConnections: %v %+v", err, conns)
	}

	if _, err = c.DeleteConnection(ctx, &awsar.DeleteConnectionInput{ConnectionArn: conn.Connection.ConnectionArn}); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}

	vc, err := c.CreateVpcConnector(ctx, &awsar.CreateVpcConnectorInput{VpcConnectorName: aws.String("vpc-conn"), Subnets: []string{subnet}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.DescribeVpcConnector(ctx, &awsar.DescribeVpcConnectorInput{VpcConnectorArn: vc.VpcConnector.VpcConnectorArn})
	if err != nil || aws.ToString(got.VpcConnector.VpcConnectorName) != "vpc-conn" {
		t.Fatalf("DescribeVpcConnector: %v %+v", err, got)
	}

	vcs, err := c.ListVpcConnectors(ctx, &awsar.ListVpcConnectorsInput{})
	if err != nil || len(vcs.VpcConnectors) != 1 {
		t.Fatalf("ListVpcConnectors: %v %+v", err, vcs)
	}

	obs, err := c.CreateObservabilityConfiguration(ctx, &awsar.CreateObservabilityConfigurationInput{
		ObservabilityConfigurationName: aws.String("xray"), TraceConfiguration: &artypes.TraceConfiguration{Vendor: artypes.TracingVendorAwsxray},
	})
	if err != nil {
		t.Fatal(err)
	}

	d, err := c.DescribeObservabilityConfiguration(ctx, &awsar.DescribeObservabilityConfigurationInput{
		ObservabilityConfigurationArn: obs.ObservabilityConfiguration.ObservabilityConfigurationArn,
	})
	if err != nil || aws.ToString(d.ObservabilityConfiguration.ObservabilityConfigurationName) != "xray" {
		t.Fatalf("DescribeObservabilityConfiguration: %v %+v", err, d)
	}

	ol, err := c.ListObservabilityConfigurations(ctx, &awsar.ListObservabilityConfigurationsInput{})
	if err != nil || len(ol.ObservabilityConfigurationSummaryList) != 1 {
		t.Fatalf("ListObservabilityConfigurations: %v %+v", err, ol)
	}

	svc, err := c.CreateService(ctx, &awsar.CreateServiceInput{
		ServiceName: aws.String("code-svc"),
		SourceConfiguration: &artypes.SourceConfiguration{
			AuthenticationConfiguration: &artypes.AuthenticationConfiguration{ConnectionArn: conn.Connection.ConnectionArn},
			CodeRepository: &artypes.CodeRepository{
				RepositoryUrl:     aws.String("https://github.com/o/r"),
				SourceCodeVersion: &artypes.SourceCodeVersion{Type: artypes.SourceCodeVersionTypeBranch, Value: aws.String("main")},
				CodeConfiguration: &artypes.CodeConfiguration{
					ConfigurationSource: artypes.ConfigurationSourceApi,
					CodeConfigurationValues: &artypes.CodeConfigurationValues{
						Runtime: artypes.RuntimePython3, BuildCommand: aws.String("pip install"), StartCommand: aws.String("python app.py"),
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateService from a code repository: %v", err)
	}

	up, err := c.UpdateService(ctx, &awsar.UpdateServiceInput{
		ServiceArn: svc.Service.ServiceArn, InstanceConfiguration: &artypes.InstanceConfiguration{Cpu: aws.String("2048"), Memory: aws.String("4096")},
	})
	if err != nil || aws.ToString(up.Service.InstanceConfiguration.Cpu) != "2048" {
		t.Fatalf("UpdateService: %v %+v", err, up)
	}

	arn := svc.Service.ServiceArn
	if _, err = c.TagResource(ctx, &awsar.TagResourceInput{ResourceArn: arn, Tags: []artypes.Tag{{Key: aws.String("a"), Value: aws.String("b")}}}); err != nil {
		t.Fatal(err)
	}

	if _, err = c.UntagResource(ctx, &awsar.UntagResourceInput{ResourceArn: arn, TagKeys: []string{"a"}}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsar.ListTagsForResourceInput{ResourceArn: arn})
	if err != nil || len(tags.Tags) != 0 {
		t.Fatalf("tags after untag: %v %+v", err, tags)
	}
}
