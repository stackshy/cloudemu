package appflow_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsappflow "github.com/aws/aws-sdk-go-v2/service/appflow"
	aftypes "github.com/aws/aws-sdk-go-v2/service/appflow/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsappflow.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{AppFlow: cloud.AppFlow})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsappflow.NewFromConfig(cfg, func(o *awsappflow.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// s3FlowInput builds a minimal S3->S3 OnDemand flow input (needs no connector
// profile), matching the canonical Terraform aws_appflow_flow example.
func s3FlowInput(name, description string) *awsappflow.CreateFlowInput {
	return &awsappflow.CreateFlowInput{
		FlowName:    aws.String(name),
		Description: aws.String(description),
		SourceFlowConfig: &aftypes.SourceFlowConfig{
			ConnectorType: aftypes.ConnectorTypeS3,
			SourceConnectorProperties: &aftypes.SourceConnectorProperties{
				S3: &aftypes.S3SourceProperties{
					BucketName:   aws.String("cloudemu-source"),
					BucketPrefix: aws.String("in"),
				},
			},
		},
		DestinationFlowConfigList: []aftypes.DestinationFlowConfig{{
			ConnectorType: aftypes.ConnectorTypeS3,
			DestinationConnectorProperties: &aftypes.DestinationConnectorProperties{
				S3: &aftypes.S3DestinationProperties{
					BucketName: aws.String("cloudemu-dest"),
				},
			},
		}},
		Tasks: []aftypes.Task{{
			TaskType:         aftypes.TaskTypeMap,
			SourceFields:     []string{"id"},
			DestinationField: aws.String("id"),
			ConnectorOperator: &aftypes.ConnectorOperator{
				S3: aftypes.S3ConnectorOperatorNoOp,
			},
		}},
		TriggerConfig: &aftypes.TriggerConfig{
			TriggerType: aftypes.TriggerTypeOndemand,
		},
		Tags: map[string]string{"env": "test"},
	}
}

func TestSDKFlowLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateFlow(ctx, s3FlowInput("sdk-flow", "first"))
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	arn := aws.ToString(create.FlowArn)
	if arn == "" {
		t.Fatal("flowArn empty")
	}

	if create.FlowStatus != aftypes.FlowStatusActive {
		t.Fatalf("flowStatus = %q, want Active", create.FlowStatus)
	}

	d1 := describe(t, c, "sdk-flow")
	assertFlowConfig(t, d1)

	if aws.ToString(d1.FlowArn) != arn {
		t.Fatalf("describe flowArn = %q, want %q", aws.ToString(d1.FlowArn), arn)
	}

	if d1.CreatedAt == nil {
		t.Fatal("createdAt nil")
	}

	// Update the description; the computed fields must stay stable.
	if _, err := c.UpdateFlow(ctx, updateInput("sdk-flow", "second")); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}

	d2 := describe(t, c, "sdk-flow")

	if aws.ToString(d2.FlowArn) != arn {
		t.Fatalf("flowArn drifted after update: %q != %q", aws.ToString(d2.FlowArn), arn)
	}

	if !d2.CreatedAt.Equal(*d1.CreatedAt) {
		t.Fatalf("createdAt drifted: %v != %v", d2.CreatedAt, d1.CreatedAt)
	}

	if aws.ToString(d2.Description) != "second" {
		t.Fatalf("description = %q, want second", aws.ToString(d2.Description))
	}

	// The nested config blocks must round-trip byte-identically across reads.
	if !reflect.DeepEqual(d1.SourceFlowConfig, d2.SourceFlowConfig) {
		t.Fatal("sourceFlowConfig drifted across reads")
	}

	if !reflect.DeepEqual(d1.Tasks, d2.Tasks) {
		t.Fatal("tasks drifted across reads")
	}

	if !reflect.DeepEqual(d1.TriggerConfig, d2.TriggerConfig) {
		t.Fatal("triggerConfig drifted across reads")
	}
}

func updateInput(name, description string) *awsappflow.UpdateFlowInput {
	in := s3FlowInput(name, description)

	return &awsappflow.UpdateFlowInput{
		FlowName:                  in.FlowName,
		Description:               in.Description,
		SourceFlowConfig:          in.SourceFlowConfig,
		DestinationFlowConfigList: in.DestinationFlowConfigList,
		Tasks:                     in.Tasks,
		TriggerConfig:             in.TriggerConfig,
	}
}

func describe(t *testing.T, c *awsappflow.Client, name string) *awsappflow.DescribeFlowOutput {
	t.Helper()

	out, err := c.DescribeFlow(context.Background(), &awsappflow.DescribeFlowInput{
		FlowName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("DescribeFlow: %v", err)
	}

	return out
}

func assertFlowConfig(t *testing.T, d *awsappflow.DescribeFlowOutput) {
	t.Helper()

	if d.SourceFlowConfig == nil || d.SourceFlowConfig.ConnectorType != aftypes.ConnectorTypeS3 {
		t.Fatal("sourceFlowConfig connectorType not preserved")
	}

	if d.SourceFlowConfig.SourceConnectorProperties.S3 == nil ||
		aws.ToString(d.SourceFlowConfig.SourceConnectorProperties.S3.BucketName) != "cloudemu-source" {
		t.Fatal("source S3 bucketName not preserved")
	}

	if len(d.DestinationFlowConfigList) != 1 {
		t.Fatalf("destinationFlowConfigList len = %d, want 1", len(d.DestinationFlowConfigList))
	}

	if len(d.Tasks) != 1 || d.Tasks[0].TaskType != aftypes.TaskTypeMap {
		t.Fatal("tasks not preserved")
	}

	if d.TriggerConfig == nil || d.TriggerConfig.TriggerType != aftypes.TriggerTypeOndemand {
		t.Fatal("triggerConfig not preserved")
	}
}

func TestSDKListAndDelete(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateFlow(ctx, s3FlowInput("list-flow", "x")); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	list, err := c.ListFlows(ctx, &awsappflow.ListFlowsInput{})
	if err != nil {
		t.Fatalf("ListFlows: %v", err)
	}

	if len(list.Flows) != 1 {
		t.Fatalf("flows len = %d, want 1", len(list.Flows))
	}

	if list.Flows[0].SourceConnectorType != aftypes.ConnectorTypeS3 ||
		list.Flows[0].DestinationConnectorType != aftypes.ConnectorTypeS3 ||
		list.Flows[0].TriggerType != aftypes.TriggerTypeOndemand {
		t.Fatal("ListFlows summary connector/trigger types not derived")
	}

	if _, err := c.DeleteFlow(ctx, &awsappflow.DeleteFlowInput{FlowName: aws.String("list-flow")}); err != nil {
		t.Fatalf("DeleteFlow: %v", err)
	}

	_, err = c.DescribeFlow(ctx, &awsappflow.DescribeFlowInput{FlowName: aws.String("list-flow")})

	var nf *aftypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeFlow after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKDuplicateFlowConflict(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateFlow(ctx, s3FlowInput("dup", "a")); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	_, err := c.CreateFlow(ctx, s3FlowInput("dup", "b"))

	var conflict *aftypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate CreateFlow: got %v, want ConflictException", err)
	}
}

func TestSDKFlowTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateFlow(ctx, s3FlowInput("tagged", "a"))
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	arn := aws.ToString(create.FlowArn)

	if _, err := c.TagResource(ctx, &awsappflow.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"team": "data"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := c.ListTagsForResource(ctx, &awsappflow.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if lt.Tags["team"] != "data" || lt.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want team=data and env=test", lt.Tags)
	}

	if _, err := c.UntagResource(ctx, &awsappflow.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	lt2, _ := c.ListTagsForResource(ctx, &awsappflow.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if _, ok := lt2.Tags["team"]; ok {
		t.Fatal("team tag not removed")
	}
}

func TestSDKConnectorProfileLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateConnectorProfile(ctx, &awsappflow.CreateConnectorProfileInput{
		ConnectorProfileName: aws.String("sf-profile"),
		ConnectorType:        aftypes.ConnectorTypeSalesforce,
		ConnectionMode:       aftypes.ConnectionModePublic,
		ConnectorProfileConfig: &aftypes.ConnectorProfileConfig{
			ConnectorProfileProperties: &aftypes.ConnectorProfileProperties{
				Salesforce: &aftypes.SalesforceConnectorProfileProperties{
					InstanceUrl: aws.String("https://example.my.salesforce.com"),
				},
			},
			ConnectorProfileCredentials: &aftypes.ConnectorProfileCredentials{
				Salesforce: &aftypes.SalesforceConnectorProfileCredentials{
					AccessToken: aws.String("tok"),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnectorProfile: %v", err)
	}

	if aws.ToString(create.ConnectorProfileArn) == "" {
		t.Fatal("connectorProfileArn empty")
	}

	desc, err := c.DescribeConnectorProfiles(ctx, &awsappflow.DescribeConnectorProfilesInput{})
	if err != nil {
		t.Fatalf("DescribeConnectorProfiles: %v", err)
	}

	if len(desc.ConnectorProfileDetails) != 1 ||
		desc.ConnectorProfileDetails[0].ConnectorType != aftypes.ConnectorTypeSalesforce {
		t.Fatal("connector profile not described")
	}

	if _, err := c.DeleteConnectorProfile(ctx, &awsappflow.DeleteConnectorProfileInput{
		ConnectorProfileName: aws.String("sf-profile"),
	}); err != nil {
		t.Fatalf("DeleteConnectorProfile: %v", err)
	}
}
