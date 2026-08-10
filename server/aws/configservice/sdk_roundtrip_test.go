package configservice_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cfgsdk "github.com/aws/aws-sdk-go-v2/service/configservice"
	cfgtypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func nowUTC() time.Time { return time.Now().UTC() }

func newConfigClient(t *testing.T) *cfgsdk.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Config: cloud.Config, AccountID: "123456789012", Region: "us-east-1"})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cfgsdk.NewFromConfig(cfg, func(o *cfgsdk.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKRecorderAndChannelLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationRecorder(ctx, &cfgsdk.PutConfigurationRecorderInput{
		ConfigurationRecorder: &cfgtypes.ConfigurationRecorder{
			Name:    aws.String("default"),
			RoleARN: aws.String("arn:aws:iam::123456789012:role/config"),
			RecordingGroup: &cfgtypes.RecordingGroup{
				AllSupported: true, IncludeGlobalResourceTypes: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("PutConfigurationRecorder: %v", err)
	}

	_, err = c.PutDeliveryChannel(ctx, &cfgsdk.PutDeliveryChannelInput{
		DeliveryChannel: &cfgtypes.DeliveryChannel{
			Name: aws.String("default"), S3BucketName: aws.String("my-config-bucket"),
		},
	})
	if err != nil {
		t.Fatalf("PutDeliveryChannel: %v", err)
	}

	if _, err = c.StartConfigurationRecorder(ctx, &cfgsdk.StartConfigurationRecorderInput{
		ConfigurationRecorderName: aws.String("default"),
	}); err != nil {
		t.Fatalf("StartConfigurationRecorder: %v", err)
	}

	status, err := c.DescribeConfigurationRecorderStatus(ctx, &cfgsdk.DescribeConfigurationRecorderStatusInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationRecorderStatus: %v", err)
	}

	if len(status.ConfigurationRecordersStatus) != 1 || !status.ConfigurationRecordersStatus[0].Recording {
		t.Fatalf("expected recording=true, got %+v", status.ConfigurationRecordersStatus)
	}

	recs, err := c.DescribeConfigurationRecorders(ctx, &cfgsdk.DescribeConfigurationRecordersInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationRecorders: %v", err)
	}

	if len(recs.ConfigurationRecorders) != 1 ||
		aws.ToString(recs.ConfigurationRecorders[0].RoleARN) != "arn:aws:iam::123456789012:role/config" {
		t.Fatalf("unexpected recorders: %+v", recs.ConfigurationRecorders)
	}

	if _, err = c.StopConfigurationRecorder(ctx, &cfgsdk.StopConfigurationRecorderInput{
		ConfigurationRecorderName: aws.String("default"),
	}); err != nil {
		t.Fatalf("StopConfigurationRecorder: %v", err)
	}

	chs, err := c.DescribeDeliveryChannels(ctx, &cfgsdk.DescribeDeliveryChannelsInput{})
	if err != nil {
		t.Fatalf("DescribeDeliveryChannels: %v", err)
	}

	if len(chs.DeliveryChannels) != 1 ||
		aws.ToString(chs.DeliveryChannels[0].S3BucketName) != "my-config-bucket" {
		t.Fatalf("unexpected channels: %+v", chs.DeliveryChannels)
	}
}

func TestSDKConfigRuleLifecycleAndTags(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigRule(ctx, &cfgsdk.PutConfigRuleInput{
		ConfigRule: &cfgtypes.ConfigRule{
			ConfigRuleName: aws.String("s3-sse"),
			Source: &cfgtypes.Source{
				Owner: cfgtypes.OwnerAws, SourceIdentifier: aws.String("S3_BUCKET_SSE_ENABLED"),
			},
			Scope: &cfgtypes.Scope{ComplianceResourceTypes: []string{"AWS::S3::Bucket"}},
		},
	})
	if err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	rules, err := c.DescribeConfigRules(ctx, &cfgsdk.DescribeConfigRulesInput{})
	if err != nil {
		t.Fatalf("DescribeConfigRules: %v", err)
	}

	if len(rules.ConfigRules) != 1 || aws.ToString(rules.ConfigRules[0].ConfigRuleName) != "s3-sse" {
		t.Fatalf("unexpected rules: %+v", rules.ConfigRules)
	}

	arn := aws.ToString(rules.ConfigRules[0].ConfigRuleArn)
	if !strings.Contains(arn, ":config:") {
		t.Fatalf("unexpected rule ARN: %s", arn)
	}

	if _, err = c.TagResource(ctx, &cfgsdk.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []cfgtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &cfgsdk.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}

	// PutEvaluations rolls compliance up to NON_COMPLIANT.
	if _, err = c.PutEvaluations(ctx, &cfgsdk.PutEvaluationsInput{
		ResultToken: aws.String("s3-sse"),
		Evaluations: []cfgtypes.Evaluation{{
			ComplianceResourceType: aws.String("AWS::S3::Bucket"),
			ComplianceResourceId:   aws.String("b1"),
			ComplianceType:         cfgtypes.ComplianceTypeNonCompliant,
			OrderingTimestamp:      aws.Time(nowUTC()),
		}},
	}); err != nil {
		t.Fatalf("PutEvaluations: %v", err)
	}

	comp, err := c.DescribeComplianceByConfigRule(ctx, &cfgsdk.DescribeComplianceByConfigRuleInput{})
	if err != nil {
		t.Fatalf("DescribeComplianceByConfigRule: %v", err)
	}

	if len(comp.ComplianceByConfigRules) != 1 ||
		comp.ComplianceByConfigRules[0].Compliance.ComplianceType != cfgtypes.ComplianceTypeNonCompliant {
		t.Fatalf("unexpected compliance: %+v", comp.ComplianceByConfigRules)
	}

	if _, err = c.DeleteConfigRule(ctx, &cfgsdk.DeleteConfigRuleInput{ConfigRuleName: aws.String("s3-sse")}); err != nil {
		t.Fatalf("DeleteConfigRule: %v", err)
	}
}

func TestSDKMissingRecorderTypedError(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.DescribeConfigurationRecorders(ctx, &cfgsdk.DescribeConfigurationRecordersInput{
		ConfigurationRecorderNames: []string{"nope"},
	})
	if err == nil {
		t.Fatal("expected error for missing recorder")
	}

	var nse *cfgtypes.NoSuchConfigurationRecorderException
	if !errors.As(err, &nse) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want NoSuchConfigurationRecorderException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want NoSuchConfigurationRecorderException, got %v", err)
	}
}

func TestSDKMissingConfigRuleTypedError(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.DescribeConfigRules(ctx, &cfgsdk.DescribeConfigRulesInput{
		ConfigRuleNames: []string{"ghost"},
	})
	if err == nil {
		t.Fatal("expected error for missing rule")
	}

	var nse *cfgtypes.NoSuchConfigRuleException
	if !errors.As(err, &nse) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want NoSuchConfigRuleException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want NoSuchConfigRuleException, got %v", err)
	}
}

func TestSDKAggregatorAndStoredQuery(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationAggregator(ctx, &cfgsdk.PutConfigurationAggregatorInput{
		ConfigurationAggregatorName: aws.String("org-agg"),
		AccountAggregationSources: []cfgtypes.AccountAggregationSource{{
			AccountIds: []string{"123456789012"}, AllAwsRegions: true,
		}},
	})
	if err != nil {
		t.Fatalf("PutConfigurationAggregator: %v", err)
	}

	aggs, err := c.DescribeConfigurationAggregators(ctx, &cfgsdk.DescribeConfigurationAggregatorsInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationAggregators: %v", err)
	}

	if len(aggs.ConfigurationAggregators) != 1 {
		t.Fatalf("want 1 aggregator, got %d", len(aggs.ConfigurationAggregators))
	}

	_, err = c.PutStoredQuery(ctx, &cfgsdk.PutStoredQueryInput{
		StoredQuery: &cfgtypes.StoredQuery{
			QueryName: aws.String("my-query"), Expression: aws.String("SELECT resourceId"),
		},
	})
	if err != nil {
		t.Fatalf("PutStoredQuery: %v", err)
	}

	got, err := c.GetStoredQuery(ctx, &cfgsdk.GetStoredQueryInput{QueryName: aws.String("my-query")})
	if err != nil {
		t.Fatalf("GetStoredQuery: %v", err)
	}

	if aws.ToString(got.StoredQuery.Expression) != "SELECT resourceId" {
		t.Fatalf("unexpected stored query: %+v", got.StoredQuery)
	}
}
